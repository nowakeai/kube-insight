#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${KUBE_INSIGHT_BIN:-${ROOT_DIR}/bin/kube-insight}"
OUTPUT_DIR="${KUBE_INSIGHT_AGENT_API_SMOKE_OUTPUT:-${ROOT_DIR}/testdata/generated/agent-api-live-smoke}"
API_LISTEN="${KUBE_INSIGHT_AGENT_API_SMOKE_API_LISTEN:-127.0.0.1:18080}"
MCP_LISTEN="${KUBE_INSIGHT_AGENT_API_SMOKE_MCP_LISTEN:-127.0.0.1:18090}"
MODEL="${KUBE_INSIGHT_AGENT_API_SMOKE_MODEL:-${MODEL:-}}"
API_KEY_ENV="${KUBE_INSIGHT_AGENT_API_SMOKE_API_KEY_ENV:-OPENAI_API_KEY}"
BASE_URL_ENV="${KUBE_INSIGHT_AGENT_API_SMOKE_BASE_URL_ENV:-OPENAI_BASE_URL}"
PROVIDER="${KUBE_INSIGHT_AGENT_API_SMOKE_PROVIDER:-openai-compatible}"
RUN_TIMEOUT_SECONDS="${KUBE_INSIGHT_AGENT_API_SMOKE_TIMEOUT_SECONDS:-360}"
QUESTIONS="${KUBE_INSIGHT_AGENT_API_SMOKE_QUESTIONS:-Is the default/api Service healthy right now? Summarize endpoint pod evidence and cite the proof.;;Map topology for namespace default and cite the proof.}"
RETRY_FIRST="${KUBE_INSIGHT_AGENT_API_SMOKE_RETRY_FIRST:-0}"
MAX_HISTORICAL_TOOL_REPLAY_CHARS="${KUBE_INSIGHT_AGENT_API_SMOKE_MAX_HISTORICAL_TOOL_REPLAY_CHARS:-4500}"
MAX_INITIAL_TOOL_CALLS="${KUBE_INSIGHT_AGENT_API_SMOKE_MAX_INITIAL_TOOL_CALLS:-0}"
MAX_FOLLOWUP_TOOL_CALLS="${KUBE_INSIGHT_AGENT_API_SMOKE_MAX_FOLLOWUP_TOOL_CALLS:-3}"
MAX_FAILED_TOOL_CALLS="${KUBE_INSIGHT_AGENT_API_SMOKE_MAX_FAILED_TOOL_CALLS:-0}"

if [[ ! -x "${BIN}" ]]; then
  echo "kube-insight binary not found or not executable: ${BIN}" >&2
  echo "Run 'make build' first or set KUBE_INSIGHT_BIN." >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required for this smoke test." >&2
  exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required for this smoke test." >&2
  exit 1
fi
if [[ -z "${MODEL}" ]]; then
  echo "Set KUBE_INSIGHT_AGENT_API_SMOKE_MODEL or MODEL." >&2
  exit 1
fi
if [[ -z "${!API_KEY_ENV:-}" ]]; then
  echo "Required API key env ${API_KEY_ENV} is not set." >&2
  exit 1
fi

mkdir -p "${OUTPUT_DIR}"
WORK_DIR="$(mktemp -d "${OUTPUT_DIR}/run.XXXXXX")"
DB_PATH="${WORK_DIR}/kubeinsight.db"
FIXTURE_PATH="${WORK_DIR}/fixture.json"
CONFIG_PATH="${WORK_DIR}/kube-insight.yaml"
SERVER_LOG="${WORK_DIR}/serve.log"
SUMMARY_PATH="${WORK_DIR}/summary.json"
REPORT_JSON_PATH="${WORK_DIR}/report.json"
REPORT_MD_PATH="${WORK_DIR}/report.md"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

validate_context_tool_order() {
  local context_json="$1"
  if ! jq -e '
    all(.requests[]?; .messages as $messages |
      [range(0; ($messages | length))] | all(. as $i |
        ($messages[$i].role != "tool") or
        ([range(0; $i)] | any(. as $j | $messages[$j].role == "assistant" and (($messages[$j].toolCalls // []) | length) > 0))
      )
    )
  ' "${context_json}" >/dev/null; then
    echo "Completion request has a tool result before any assistant tool-call: ${context_json}" >&2
    jq '.requests[] | {sequence, messages: [.messages[] | {index, role, toolCalls, toolCallId, toolName, contentPreview}]}' "${context_json}" >&2
    exit 1
  fi
}

write_smoke_report() {
  local generated_at
  generated_at="$(date -u +'%Y-%m-%dT%H:%M:%S.000Z')"
  jq -n \
    --arg generatedAt "${generated_at}" \
    --arg provider "${PROVIDER}" \
    --arg model "${MODEL}" \
    --arg sessionId "${session_id}" \
    --arg workDir "${WORK_DIR}" \
    --arg summaryPath "${SUMMARY_PATH}" \
    --slurpfile runs "${SUMMARY_PATH}" '
      ($runs[0] // []) as $items
      | {
          generatedAt: $generatedAt,
          provider: $provider,
          model: $model,
          sessionId: $sessionId,
          workDir: $workDir,
          summaryPath: $summaryPath,
          totals: {
            runs: ($items | length),
            completedRuns: ($items | map(select(.status == "run.completed")) | length),
            toolCalls: (($items | map(.toolCalls // 0) | add) // 0),
            failedToolCalls: (($items | map(.failedToolCalls // 0) | add) // 0),
            completionRequests: (($items | map(.completionRequests // 0) | add) // 0),
            artifacts: (($items | map(.artifacts // 0) | add) // 0),
            citations: (($items | map(.citations // 0) | add) // 0),
            runsWithCitations: ($items | map(select((.citations // 0) > 0)) | length),
            childRunIds: (($items | map(.childRunIds // 0) | add) // 0),
            maxContextMessages: (($items | map(.contextMessages // 0) | max) // 0),
            maxInitialContextTotalChars: (($items | map(.initialContext.totalChars // 0) | max) // 0),
            maxInitialContextToolChars: (($items | map(.initialContext.toolChars // 0) | max) // 0),
            maxInitialContextMaxToolChars: (($items | map(.initialContext.maxToolChars // 0) | max) // 0),
            compactedToolResults: (($items | map(.initialContext.compactedToolResults // 0) | add) // 0)
          },
          toolFrequency: (
            [$items[]? | .toolNames[]?]
            | sort
            | group_by(.)
            | map({name: .[0], count: length})
            | sort_by(-.count, .name)
          ),
          runs: (
            $items
            | to_entries
            | map(.value + {
                index: (.key + 1),
                shortQuestion: ((.value.question // "") | gsub("[\\r\\n]+"; " ") | if length > 96 then .[0:95] + "…" else . end)
              })
          )
        }
    ' >"${REPORT_JSON_PATH}"
  jq -r '
    def cell: tostring | gsub("\\|"; "\\|") | gsub("[\\r\\n]+"; " ");
    [
      "# Agent API Live Smoke Report",
      "",
      "- Generated: \(.generatedAt)",
      "- Provider/model: \(.provider)/\(.model)",
      "- Session: \(.sessionId)",
      "- Work dir: `\(.workDir)`",
      "",
      "## Totals",
      "",
      "| Metric | Value |",
      "| --- | ---: |",
      "| Runs | \(.totals.runs) |",
      "| Completed runs | \(.totals.completedRuns) |",
      "| Tool calls | \(.totals.toolCalls) |",
      "| Failed tool calls | \(.totals.failedToolCalls) |",
      "| Completion requests | \(.totals.completionRequests) |",
      "| Artifacts | \(.totals.artifacts) |",
      "| Citations | \(.totals.citations) |",
      "| Runs with citations | \(.totals.runsWithCitations) |",
      "| Max context messages | \(.totals.maxContextMessages) |",
      "| Max initial context chars | \(.totals.maxInitialContextTotalChars) |",
      "| Max initial tool chars | \(.totals.maxInitialContextToolChars) |",
      "| Compacted tool results | \(.totals.compactedToolResults) |",
      "",
      "## Runs",
      "",
      "| # | Status | Tools | Failed | Citations | Context msgs | Initial chars | Question |",
      "| ---: | --- | ---: | ---: | ---: | ---: | ---: | --- |"
    ][],
    (.runs[]? | "| \(.index) | \(.status | cell) | \(.toolCalls // 0) | \(.failedToolCalls // 0) | \(.citations // 0) | \(.contextMessages // 0) | \(.initialContext.totalChars // 0) | \(.shortQuestion | cell) |"),
    "",
    "## Tool Frequency",
    "",
    "| Tool | Count |",
    "| --- | ---: |",
    (.toolFrequency[]? | "| \(.name | cell) | \(.count) |")
  ' "${REPORT_JSON_PATH}" >"${REPORT_MD_PATH}"
}

cat >"${FIXTURE_PATH}" <<'JSON'
{
  "items": [
    {
      "apiVersion": "v1",
      "kind": "Service",
      "metadata": {"name": "api", "namespace": "default", "uid": "svc-api-uid"},
      "spec": {"type": "ClusterIP", "selector": {"app": "api"}, "ports": [{"port": 80, "targetPort": 8080}]}
    },
    {
      "apiVersion": "v1",
      "kind": "Pod",
      "metadata": {"name": "api-0", "namespace": "default", "uid": "pod-api-0-uid", "labels": {"app": "api"}},
      "spec": {"nodeName": "node-a", "containers": [{"name": "api", "image": "example/api:latest"}]},
      "status": {
        "phase": "Running",
        "conditions": [{"type": "Ready", "status": "False"}],
        "containerStatuses": [{"name": "api", "ready": false, "restartCount": 1, "lastState": {"terminated": {"reason": "OOMKilled", "exitCode": 137}}}]
      }
    },
    {
      "apiVersion": "v1",
      "kind": "Node",
      "metadata": {"name": "node-a", "uid": "node-a-uid", "labels": {"cloud.google.com/gke-nodepool": "default-pool"}},
      "status": {
        "capacity": {"cpu": "8", "memory": "32869472Ki", "pods": "110"},
        "allocatable": {"cpu": "7900m", "memory": "31820896Ki", "pods": "110"},
        "conditions": [{"type": "Ready", "status": "True"}]
      }
    },
    {
      "apiVersion": "discovery.k8s.io/v1",
      "kind": "EndpointSlice",
      "metadata": {"name": "api-abc", "namespace": "default", "uid": "eps-api-abc-uid", "labels": {"kubernetes.io/service-name": "api"}},
      "addressType": "IPv4",
      "ports": [{"name": "http", "port": 8080}],
      "endpoints": [{"conditions": {"ready": false}, "targetRef": {"kind": "Pod", "namespace": "default", "name": "api-0", "uid": "pod-api-0-uid"}}]
    },
    {
      "apiVersion": "events.k8s.io/v1",
      "kind": "Event",
      "metadata": {"name": "api-0-oom", "namespace": "default", "uid": "event-api-0-oom-uid"},
      "regarding": {"kind": "Pod", "namespace": "default", "name": "api-0", "uid": "pod-api-0-uid"},
      "type": "Warning",
      "reason": "OOMKilled",
      "note": "Container api was OOMKilled and restarted once",
      "eventTime": "2026-05-24T10:05:00Z"
    }
  ]
}
JSON

cat >"${CONFIG_PATH}" <<YAML
server:
  api:
    enabled: true
    listen: ${API_LISTEN}
  chat:
    enabled: true
    provider: ${PROVIDER}
    apiKeyEnv: ${API_KEY_ENV}
    baseUrlEnv: ${BASE_URL_ENV}
    model: ${MODEL}
mcp:
  enabled: true
  listen: ${MCP_LISTEN}
YAML

"${BIN}" ingest --file "${FIXTURE_PATH}" --db "${DB_PATH}" >"${WORK_DIR}/ingest.json"
export KUBE_INSIGHT_SERVER_CHAT_PROVIDER="${PROVIDER}"
export KUBE_INSIGHT_SERVER_CHAT_API_KEY_ENV="${API_KEY_ENV}"
export KUBE_INSIGHT_SERVER_CHAT_BASE_URL_ENV="${BASE_URL_ENV}"
export KUBE_INSIGHT_SERVER_CHAT_MODEL="${MODEL}"
"${BIN}" --config "${CONFIG_PATH}" serve --api --mcp --db "${DB_PATH}" --api-listen "${API_LISTEN}" --mcp-listen "${MCP_LISTEN}" >"${SERVER_LOG}" 2>&1 &
SERVER_PID="$!"

for _ in $(seq 1 60); do
  if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    echo "serve exited before becoming ready. Log:" >&2
    cat "${SERVER_LOG}" >&2
    exit 1
  fi
  if curl -fsS "http://${API_LISTEN}/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
  echo "serve exited before becoming ready. Log:" >&2
  cat "${SERVER_LOG}" >&2
  exit 1
fi
curl -fsS "http://${API_LISTEN}/healthz" >/dev/null

session_json="$(curl -fsS -X POST "http://${API_LISTEN}/api/v1/agent/sessions" \
  -H 'content-type: application/json' \
  -d "$(jq -n --arg title "API live smoke" --arg provider "${PROVIDER}" --arg model "${MODEL}" '{title:$title, provider:$provider, model:$model}')")"
session_id="$(jq -r '.id' <<<"${session_json}")"

printf '[' >"${SUMMARY_PATH}"
first_summary=1
completed_questions=()
completed_run_ids=()
IFS=';;' read -r -a question_list <<<"${QUESTIONS}"
for question in "${question_list[@]}"; do
  question="$(sed 's/^ *//;s/ *$//' <<<"${question}")"
  [[ -z "${question}" ]] && continue
  slug="$(tr '[:upper:]' '[:lower:]' <<<"${question}" | tr -cs 'a-z0-9' '-' | sed 's/^-//;s/-$//' | cut -c1-72)"
  [[ -z "${slug}" ]] && slug="question"
  client_sent_at="$(date -u +'%Y-%m-%dT%H:%M:%S.000Z')"
  run_json="$(curl -fsS -X POST "http://${API_LISTEN}/api/v1/agent/sessions/${session_id}/runs" \
    -H 'content-type: application/json' \
    -d "$(jq -n \
      --arg input "${question}" \
      --arg provider "${PROVIDER}" \
      --arg model "${MODEL}" \
      --arg sentAt "${client_sent_at}" \
      '{input:$input, provider:$provider, model:$model, metadata:{clientContext:{sentAt:$sentAt, localTime:$sentAt, timeZone:"UTC", timezoneOffsetMinutes:0, locale:"en-US"}}}')")"
  run_id="$(jq -r '.id' <<<"${run_json}")"
  sse_path="${WORK_DIR}/${slug}.sse"
  events_json="${WORK_DIR}/${slug}.events.json"
  curl -fsS --max-time "${RUN_TIMEOUT_SECONDS}" "http://${API_LISTEN}/api/v1/agent/runs/${run_id}/events?follow=true" >"${sse_path}"
  awk '/^data: /{sub(/^data: /,""); print}' "${sse_path}" | jq -s '.' >"${events_json}"
  status="$(jq -r 'map(select(.type=="run.completed" or .type=="run.failed" or .type=="run.cancelled")) | last | .type // empty' "${events_json}")"
  completion_requests="$(jq '[.[] | select(.type=="completion.request")] | length' "${events_json}")"
  tool_calls="$(jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed")]' "${events_json}")"
  tool_call_count="$(jq 'length' <<<"${tool_calls}")"
  tool_call_names="$(jq '[.[].data.name // ""] | map(select(. != ""))' <<<"${tool_calls}")"
  failed_tool_call_count="$(jq '[.[] | select(.type=="tool.failed" or .data.status=="failed")] | length' <<<"${tool_calls}")"
  artifacts="$(jq '[.[] | select(.type=="artifact.created")] | length' "${events_json}")"
  citations="$(jq '[.[] | select(.type=="citation.created")] | length' "${events_json}")"
  child_run_ids="$(jq '[.[] | select(.type=="artifact.created") | .data.artifact.data.output.branches[]? | select(.childRunId != null) | .childRunId] | unique | length' "${events_json}")"
  final_answer="$(jq -r '[.[] | select(.type=="answer.final" or .type=="message.completed") | .data.content // empty] | last // ""' "${events_json}")"
  if [[ "${status}" != "run.completed" ]]; then
    echo "Run ${run_id} did not complete successfully: ${status}" >&2
    cat "${sse_path}" >&2
    exit 1
  fi
  if (( failed_tool_call_count > MAX_FAILED_TOOL_CALLS )); then
    echo "Run ${run_id} exceeded failed tool-call budget: failed=${failed_tool_call_count} max=${MAX_FAILED_TOOL_CALLS}" >&2
    jq '[.[] | select(.type=="tool.failed" or .data.status=="failed") | {type, sequence, data}]' "${events_json}" >&2
    exit 1
  fi
  if [[ "${#completed_questions[@]}" -eq 0 ]] && (( MAX_INITIAL_TOOL_CALLS > 0 )) && (( tool_call_count > MAX_INITIAL_TOOL_CALLS )); then
    echo "Initial run ${run_id} exceeded tool-call budget: tools=${tool_call_count} max=${MAX_INITIAL_TOOL_CALLS}" >&2
    jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed") | {type, sequence, name:.data.name, status:.data.status, summary:.data.outputSummary}]' "${events_json}" >&2
    exit 1
  fi
  if [[ "${#completed_questions[@]}" -gt 0 ]] && (( tool_call_count > MAX_FOLLOWUP_TOOL_CALLS )); then
    echo "Follow-up run ${run_id} exceeded tool-call budget: tools=${tool_call_count} max=${MAX_FOLLOWUP_TOOL_CALLS}" >&2
    jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed") | {type, sequence, name:.data.name, status:.data.status, summary:.data.outputSummary}]' "${events_json}" >&2
    exit 1
  fi
  if [[ "${completion_requests}" -lt 1 ]]; then
    echo "Run ${run_id} did not emit completion.request." >&2
    jq 'map({type, sequence, data})' "${events_json}" >&2
    exit 1
  fi
  context_json="${WORK_DIR}/${slug}.context.json"
  "${BIN}" --db "${DB_PATH}" db agent-context "${run_id}" --all --output json >"${context_json}"
  initial_context_metrics="$(jq '
    ((.requests[0] // {messages: []}).messages // []) as $messages
    | ($messages | map(select(.role == "tool"))) as $toolMessages
    | {
      messages: ($messages | length),
      totalChars: (($messages | map(.contentChars // 0) | add) // 0),
      toolChars: (($toolMessages | map(.contentChars // 0) | add) // 0),
      maxToolChars: (($toolMessages | map(.contentChars // 0) | max) // 0),
      compactedToolResults: ($toolMessages | map(select((.content // "") | contains("kube-insight.agent.compacted_tool_result.v1"))) | length)
    }
  ' "${context_json}")"
  validate_context_tool_order "${context_json}"
  if [[ "${#completed_questions[@]}" -gt 0 ]]; then
    if ! jq -e --argjson maxChars "${MAX_HISTORICAL_TOOL_REPLAY_CHARS}" '
      all(.requests[0].messages[]? | select(.role=="tool"); (.contentChars // 0) <= $maxChars)
    ' "${context_json}" >/dev/null; then
      echo "Follow-up initial completion.request has oversized historical tool result: ${context_json}" >&2
      jq --argjson maxChars "${MAX_HISTORICAL_TOOL_REPLAY_CHARS}" '.requests[0].messages[]? | select(.role=="tool" and ((.contentChars // 0) > $maxChars)) | {index, role, toolName, toolCallId, contentChars, contentPreview}' "${context_json}" >&2
      exit 1
    fi
  fi
  if ! jq -e --arg question "${question}" '.requests[-1].messages[]? | select(.role=="user" and .content==$question)' "${context_json}" >/dev/null; then
    echo "Run ${run_id} latest completion.request is missing current user message: ${question}" >&2
    jq '.requests[-1].messages | map({index, role, contentPreview})' "${context_json}" >&2
    exit 1
  fi
  for prior_question in "${completed_questions[@]}"; do
    if ! jq -e --arg question "${prior_question}" '.requests[-1].messages[]? | select(.role=="user" and .content==$question)' "${context_json}" >/dev/null; then
      echo "Run ${run_id} latest completion.request is missing prior user message: ${prior_question}" >&2
      jq '.requests[-1].messages | map({index, role, contentPreview})' "${context_json}" >&2
      exit 1
    fi
  done
  if [[ "${#completed_questions[@]}" -gt 0 ]]; then
    if ! jq -e --arg question "${question}" '
      .requests[-1].messages as $messages
      | ($messages | map(.content) | index($question)) as $currentIndex
      | $currentIndex != null
      and any($messages[0:$currentIndex][]; .role=="assistant" and (((.content // "") | length) > 0 or ((.toolCalls // []) | length) > 0))
    ' "${context_json}" >/dev/null; then
      echo "Run ${run_id} latest completion.request has no assistant context before current user message." >&2
      jq '.requests[-1].messages | map({index, role, contentPreview, toolCalls})' "${context_json}" >&2
      exit 1
    fi
  fi
  if [[ "${artifacts}" -lt 1 || "${citations}" -lt 1 || -z "${final_answer}" ]]; then
    echo "Run ${run_id} missing expected final answer/artifact/citation." >&2
    jq '{events:length, completionRequests:[.[]|select(.type=="completion.request")], artifacts:[.[]|select(.type=="artifact.created")], citations:[.[]|select(.type=="citation.created")], finals:[.[]|select(.type=="answer.final" or .type=="message.completed")]}' "${events_json}" >&2
    exit 1
  fi
  if [[ "${first_summary}" -eq 0 ]]; then
    printf ',' >>"${SUMMARY_PATH}"
  fi
  first_summary=0
  jq -n \
    --arg question "${question}" \
    --arg runId "${run_id}" \
    --arg status "${status}" \
    --argjson completionRequests "${completion_requests}" \
    --argjson contextMessages "$(jq '.requests[-1].messageCount' "${context_json}")" \
    --argjson initialContext "${initial_context_metrics}" \
    --argjson toolCalls "${tool_call_count}" \
    --argjson failedToolCalls "${failed_tool_call_count}" \
    --argjson toolNames "${tool_call_names}" \
    --argjson artifacts "${artifacts}" \
    --argjson citations "${citations}" \
    --argjson childRunIds "${child_run_ids}" \
    --arg eventsPath "${events_json}" \
    --arg contextPath "${context_json}" \
    '{question:$question, runId:$runId, status:$status, completionRequests:$completionRequests, contextMessages:$contextMessages, initialContext:$initialContext, toolCalls:$toolCalls, failedToolCalls:$failedToolCalls, toolNames:$toolNames, artifacts:$artifacts, citations:$citations, childRunIds:$childRunIds, eventsPath:$eventsPath, contextPath:$contextPath}' >>"${SUMMARY_PATH}"
  completed_questions+=("${question}")
  completed_run_ids+=("${run_id}")
done
if [[ "${RETRY_FIRST}" == "1" || "${RETRY_FIRST}" == "true" ]]; then
  if [[ "${#completed_run_ids[@]}" -lt 1 ]]; then
    echo "KUBE_INSIGHT_AGENT_API_SMOKE_RETRY_FIRST requires at least one completed question." >&2
    exit 1
  fi
  original_run_id="${completed_run_ids[0]}"
  original_question="${completed_questions[0]}"
  retry_json="$(curl -fsS -X POST "http://${API_LISTEN}/api/v1/agent/runs/${original_run_id}/retry" \
    -H 'content-type: application/json' \
    -d "$(jq -n \
      --arg sentAt "$(date -u +'%Y-%m-%dT%H:%M:%S.000Z')" \
      '{metadata:{clientContext:{sentAt:$sentAt, localTime:$sentAt, timeZone:"UTC", timezoneOffsetMinutes:0, locale:"en-US"}}}')")"
  retry_run_id="$(jq -r '.id' <<<"${retry_json}")"
  if ! jq -e --arg retryOf "${original_run_id}" '.metadata.retryOfRunId == $retryOf and .metadata.retryRootRunId == $retryOf' <<<"${retry_json}" >/dev/null; then
    echo "Retry run ${retry_run_id} metadata does not point at original run ${original_run_id}." >&2
    jq '.metadata' <<<"${retry_json}" >&2
    exit 1
  fi
  retry_sse_path="${WORK_DIR}/retry-first.sse"
  retry_events_json="${WORK_DIR}/retry-first.events.json"
  curl -fsS --max-time "${RUN_TIMEOUT_SECONDS}" "http://${API_LISTEN}/api/v1/agent/runs/${retry_run_id}/events?follow=true" >"${retry_sse_path}"
  awk '/^data: /{sub(/^data: /,""); print}' "${retry_sse_path}" | jq -s '.' >"${retry_events_json}"
  retry_status="$(jq -r 'map(select(.type=="run.completed" or .type=="run.failed" or .type=="run.cancelled")) | last | .type // empty' "${retry_events_json}")"
  retry_completion_requests="$(jq '[.[] | select(.type=="completion.request")] | length' "${retry_events_json}")"
  if [[ "${retry_status}" != "run.completed" || "${retry_completion_requests}" -lt 1 ]]; then
    echo "Retry run ${retry_run_id} did not complete with completion.request events." >&2
    jq 'map({type, sequence, data})' "${retry_events_json}" >&2
    exit 1
  fi
  retry_context_json="${WORK_DIR}/retry-first.context.json"
  "${BIN}" --db "${DB_PATH}" db agent-context "${retry_run_id}" --all --output json >"${retry_context_json}"
  validate_context_tool_order "${retry_context_json}"
  if ! jq -e --arg question "${original_question}" '.requests[0].messages[]? | select(.role=="user" and .content==$question)' "${retry_context_json}" >/dev/null; then
    echo "Retry run ${retry_run_id} first completion.request is missing original user message: ${original_question}" >&2
    jq '.requests[0].messages | map({index, role, contentPreview})' "${retry_context_json}" >&2
    exit 1
  fi
  if ! jq -e 'all(.requests[0].messages[]?; .role != "assistant" and .role != "tool")' "${retry_context_json}" >/dev/null; then
    echo "Retry run ${retry_run_id} first completion.request includes pre-retry assistant/tool context." >&2
    jq '.requests[0].messages | map({index, role, contentPreview, toolCalls})' "${retry_context_json}" >&2
    exit 1
  fi
  if [[ "${#completed_questions[@]}" -gt 1 ]]; then
    for later_question in "${completed_questions[@]:1}"; do
      if jq -e --arg question "${later_question}" '.requests[0].messages[]? | select(.role=="user" and .content==$question)' "${retry_context_json}" >/dev/null; then
        echo "Retry run ${retry_run_id} first completion.request includes later user message: ${later_question}" >&2
        jq '.requests[0].messages | map({index, role, contentPreview})' "${retry_context_json}" >&2
        exit 1
      fi
    done
  fi
  session_after_retry_json="${WORK_DIR}/retry-first.session.json"
  curl -fsS "http://${API_LISTEN}/api/v1/agent/sessions/${session_id}" >"${session_after_retry_json}"
  if ! jq -e --arg runId "${retry_run_id}" --arg retryOf "${original_run_id}" '
    any(.runs[]?; .id == $runId and .metadata.retryOfRunId == $retryOf and .metadata.retryRootRunId == $retryOf)
  ' "${session_after_retry_json}" >/dev/null; then
    echo "Session ${session_id} does not expose retry replacement run ${retry_run_id} with retry metadata." >&2
    jq '{id, runs: [.runs[]? | {id, input, status, metadata}]}' "${session_after_retry_json}" >&2
    exit 1
  fi
  if [[ "${first_summary}" -eq 0 ]]; then
    printf ',' >>"${SUMMARY_PATH}"
  fi
  first_summary=0
  jq -n \
    --arg question "retry:${original_question}" \
    --arg runId "${retry_run_id}" \
    --arg retryOfRunId "${original_run_id}" \
    --arg status "${retry_status}" \
    --argjson completionRequests "${retry_completion_requests}" \
    --argjson contextMessages "$(jq '.requests[0].messageCount' "${retry_context_json}")" \
    --arg eventsPath "${retry_events_json}" \
    --arg contextPath "${retry_context_json}" \
    --arg sessionPath "${session_after_retry_json}" \
    --argjson sessionRunCount "$(jq '.runs | length' "${session_after_retry_json}")" \
    '{question:$question, runId:$runId, retryOfRunId:$retryOfRunId, status:$status, completionRequests:$completionRequests, contextMessages:$contextMessages, sessionRunCount:$sessionRunCount, eventsPath:$eventsPath, contextPath:$contextPath, sessionPath:$sessionPath}' >>"${SUMMARY_PATH}"
fi
printf ']\n' >>"${SUMMARY_PATH}"
write_smoke_report

echo "API live smoke passed. Summary: ${SUMMARY_PATH}"
echo "API live smoke report: ${REPORT_MD_PATH}"
