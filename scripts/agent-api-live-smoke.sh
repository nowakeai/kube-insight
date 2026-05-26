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
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

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
  if curl -fsS "http://${API_LISTEN}/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    echo "serve exited before becoming ready. Log:" >&2
    cat "${SERVER_LOG}" >&2
    exit 1
  fi
  sleep 1
done
curl -fsS "http://${API_LISTEN}/healthz" >/dev/null

session_json="$(curl -fsS -X POST "http://${API_LISTEN}/api/v1/agent/sessions" \
  -H 'content-type: application/json' \
  -d "$(jq -n --arg title "API live smoke" --arg provider "${PROVIDER}" --arg model "${MODEL}" '{title:$title, provider:$provider, model:$model}')")"
session_id="$(jq -r '.id' <<<"${session_json}")"

printf '[' >"${SUMMARY_PATH}"
first_summary=1
IFS=';;' read -r -a question_list <<<"${QUESTIONS}"
for question in "${question_list[@]}"; do
  question="$(sed 's/^ *//;s/ *$//' <<<"${question}")"
  [[ -z "${question}" ]] && continue
  slug="$(tr '[:upper:]' '[:lower:]' <<<"${question}" | tr -cs 'a-z0-9' '-' | sed 's/^-//;s/-$//' | cut -c1-72)"
  [[ -z "${slug}" ]] && slug="question"
  run_json="$(curl -fsS -X POST "http://${API_LISTEN}/api/v1/agent/sessions/${session_id}/runs" \
    -H 'content-type: application/json' \
    -d "$(jq -n --arg input "${question}" --arg provider "${PROVIDER}" --arg model "${MODEL}" '{input:$input, provider:$provider, model:$model}')")"
  run_id="$(jq -r '.id' <<<"${run_json}")"
  sse_path="${WORK_DIR}/${slug}.sse"
  events_json="${WORK_DIR}/${slug}.events.json"
  curl -fsS --max-time "${RUN_TIMEOUT_SECONDS}" "http://${API_LISTEN}/api/v1/agent/runs/${run_id}/events?follow=true" >"${sse_path}"
  awk '/^data: /{sub(/^data: /,""); print}' "${sse_path}" | jq -s '.' >"${events_json}"
  status="$(jq -r 'map(select(.type=="run.completed" or .type=="run.failed" or .type=="run.cancelled")) | last | .type // empty' "${events_json}")"
  completion_requests="$(jq '[.[] | select(.type=="completion.request")] | length' "${events_json}")"
  artifacts="$(jq '[.[] | select(.type=="artifact.created")] | length' "${events_json}")"
  citations="$(jq '[.[] | select(.type=="citation.created")] | length' "${events_json}")"
  child_run_ids="$(jq '[.[] | select(.type=="artifact.created") | .data.artifact.data.output.branches[]? | select(.childRunId != null) | .childRunId] | unique | length' "${events_json}")"
  final_answer="$(jq -r '[.[] | select(.type=="answer.final" or .type=="message.completed") | .data.content // empty] | last // ""' "${events_json}")"
  if [[ "${status}" != "run.completed" ]]; then
    echo "Run ${run_id} did not complete successfully: ${status}" >&2
    cat "${sse_path}" >&2
    exit 1
  fi
  if [[ "${completion_requests}" -lt 1 ]]; then
    echo "Run ${run_id} did not emit completion.request." >&2
    jq 'map({type, sequence, data})' "${events_json}" >&2
    exit 1
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
    --argjson artifacts "${artifacts}" \
    --argjson citations "${citations}" \
    --argjson childRunIds "${child_run_ids}" \
    --arg eventsPath "${events_json}" \
    '{question:$question, runId:$runId, status:$status, completionRequests:$completionRequests, artifacts:$artifacts, citations:$citations, childRunIds:$childRunIds, eventsPath:$eventsPath}' >>"${SUMMARY_PATH}"
done
printf ']\n' >>"${SUMMARY_PATH}"

echo "API live smoke passed. Summary: ${SUMMARY_PATH}"
