#!/usr/bin/env bash
set -euo pipefail

API_URL=${KUBE_INSIGHT_AGENT_CASE_API_URL:-http://127.0.0.1:8080}
OUT_DIR=${KUBE_INSIGHT_AGENT_CASE_OUTPUT:-testdata/generated/agent-clickhouse-case-smoke}
RUN_AGENT_CASE=${KUBE_INSIGHT_AGENT_CASE_RUN_AGENT:-0}
AGENT_CASE_QUESTION=${KUBE_INSIGHT_AGENT_CASE_QUESTION:-看看 gcp2半天内集群的节点是否有变化？一共有多少节点，有哪些类型？总的cpu 内存 量是多少}
AGENT_CASE_TIMEOUT=${KUBE_INSIGHT_AGENT_CASE_TIMEOUT_SECONDS:-360}
AGENT_CASE_REQUIRED_TOOLS=${KUBE_INSIGHT_AGENT_CASE_REQUIRED_TOOLS:-kube_insight_health,kube_insight_schema,kube_insight_js}
AGENT_CASE_FORBIDDEN_TOOLS=${KUBE_INSIGHT_AGENT_CASE_FORBIDDEN_TOOLS:-artifact_transform_js,kube_insight_scripted_query}
AGENT_CASE_EXPECTED_TOOL_SEQUENCE=${KUBE_INSIGHT_AGENT_CASE_EXPECTED_TOOL_SEQUENCE:-kube_insight_health,kube_insight_schema,kube_insight_js;kube_insight_schema,kube_insight_health,kube_insight_js}
AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT=${KUBE_INSIGHT_AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT:-gcp2}
AGENT_CASE_REQUIRED_ANSWER_TERMS=${KUBE_INSIGHT_AGENT_CASE_REQUIRED_ANSWER_TERMS:-节点,CPU,GiB}
AGENT_CASE_FORBIDDEN_ANSWER_TERMS=${KUBE_INSIGHT_AGENT_CASE_FORBIDDEN_ANSWER_TERMS:-没有净增减}
AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS=${KUBE_INSIGHT_AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS:-北京时间|Asia/Shanghai|+08:00}
AGENT_CASE_CLIENT_TIME_ZONE=${KUBE_INSIGHT_AGENT_CASE_CLIENT_TIME_ZONE:-Asia/Shanghai}
AGENT_CASE_CLIENT_UTC_OFFSET_MINUTES=${KUBE_INSIGHT_AGENT_CASE_CLIENT_UTC_OFFSET_MINUTES:-480}
AGENT_CASE_CLIENT_LOCALE=${KUBE_INSIGHT_AGENT_CASE_CLIENT_LOCALE:-zh-CN}
mkdir -p "$OUT_DIR"

request_json() {
  local method=$1
  local path=$2
  local body=${3:-}
  if [[ -n "$body" ]]; then
    curl -fsS -X "$method" "$API_URL$path" -H 'content-type: application/json' -d "$body"
  else
    curl -fsS -X "$method" "$API_URL$path"
  fi
}

sql_case() {
  local name=$1
  local sql=$2
  local outfile="$OUT_DIR/$name.json"
  printf 'running %s\n' "$name" >&2
  request_json POST /api/v1/sql "$(printf '{"sql":%s,"maxRows":25}' "$(printf '%s' "$sql" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')")" > "$outfile"
  python3 - "$outfile" <<'PY'
import json, sys
path = sys.argv[1]
with open(path) as f:
    data = json.load(f)
if 'error' in data:
    raise SystemExit(f"SQL case failed: {data['error']}")
print(f"{path}: rows={data.get('rowCount', len(data.get('rows', [])))} truncated={data.get('truncated', False)}")
PY
}

csv_each() {
  local raw=$1
  tr ',' '\n' <<<"$raw" | sed 's/^ *//;s/ *$//' | sed '/^$/d'
}

pipe_each() {
  local raw=$1
  tr '|' '\n' <<<"$raw" | sed 's/^ *//;s/ *$//' | sed '/^$/d'
}

agent_case() {
  local session_json session_id run_json run_id slug sse_path events_json status final_answer

  printf 'running agent case: %s\n' "$AGENT_CASE_QUESTION" >&2
  session_json="$(request_json POST /api/v1/agent/sessions "$(jq -n --arg title "ClickHouse agent case smoke" '{title:$title}')")"
  session_id="$(jq -r '.id' <<<"$session_json")"
  local client_sent_at client_context_json
  client_sent_at="${KUBE_INSIGHT_AGENT_CASE_CLIENT_SENT_AT:-$(date -u +'%Y-%m-%dT%H:%M:%S.000Z')}"
  client_context_json="$(python3 -c '
import json, sys
from datetime import datetime, timedelta, timezone
sent_at, zone, offset_minutes, locale = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
parsed = datetime.fromisoformat(sent_at.replace("Z", "+00:00"))
if parsed.tzinfo is None:
    parsed = parsed.replace(tzinfo=timezone.utc)
client_tz = timezone(timedelta(minutes=offset_minutes))
print(json.dumps({
    "sentAt": sent_at,
    "localTime": parsed.astimezone(client_tz).isoformat(timespec="milliseconds"),
    "timeZone": zone,
    "timezoneOffsetMinutes": offset_minutes,
    "locale": locale,
}))
' "$client_sent_at" "$AGENT_CASE_CLIENT_TIME_ZONE" "$AGENT_CASE_CLIENT_UTC_OFFSET_MINUTES" "$AGENT_CASE_CLIENT_LOCALE")"
  run_json="$(request_json POST "/api/v1/agent/sessions/$session_id/runs" "$(jq -n \
    --arg input "$AGENT_CASE_QUESTION" \
    --argjson clientContext "$client_context_json" \
    '{input:$input, metadata:{clientContext:$clientContext}}')")"
  run_id="$(jq -r '.id' <<<"$run_json")"
  slug="agent-gcp2-half-day-node-capacity"
  sse_path="$OUT_DIR/$slug.sse"
  events_json="$OUT_DIR/$slug.events.json"

  curl -fsS --max-time "$AGENT_CASE_TIMEOUT" "$API_URL/api/v1/agent/runs/$run_id/events?follow=true" >"$sse_path"
  awk '/^data: /{sub(/^data: /,""); print}' "$sse_path" | jq -s '.' >"$events_json"
  status="$(jq -r 'map(select(.type=="run.completed" or .type=="run.failed" or .type=="run.cancelled")) | last | .type // empty' "$events_json")"
  final_answer="$(jq -r '[.[] | select(.type=="answer.final" or .type=="message.completed") | .data.content // empty] | last // ""' "$events_json")"

  if [[ "$status" != "run.completed" ]]; then
    echo "Agent run $run_id did not complete successfully: $status" >&2
    jq 'map({type, sequence, data})' "$events_json" >&2
    exit 1
  fi

  if jq -e '[.[] | select(.type=="tool.failed" or .data.status=="failed")] | length > 0' "$events_json" >/dev/null; then
    echo "Agent run $run_id has failed tool calls." >&2
    jq '[.[] | select(.type=="tool.failed" or .data.status=="failed") | {type, sequence, data}]' "$events_json" >&2
    exit 1
  fi

  while IFS= read -r tool_name; do
    if ! jq -e --arg name "$tool_name" 'any(.[]; (.type=="tool.completed" or .type=="tool.audit") and .data.name==$name and (.data.status // "completed") != "failed")' "$events_json" >/dev/null; then
      echo "Agent run $run_id did not call required tool: $tool_name" >&2
      jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") | {sequence, name:.data.name, status:.data.status, input:.data.input}]' "$events_json" >&2
      exit 1
    fi
  done < <(csv_each "$AGENT_CASE_REQUIRED_TOOLS")

  while IFS= read -r tool_name; do
    if jq -e --arg name "$tool_name" 'any(.[]; (.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") and .data.name==$name)' "$events_json" >/dev/null; then
      echo "Agent run $run_id called forbidden tool: $tool_name" >&2
      jq --arg name "$tool_name" '[.[] | select((.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") and .data.name==$name) | {sequence, name:.data.name, status:.data.status, input:.data.input}]' "$events_json" >&2
      exit 1
    fi
  done < <(csv_each "$AGENT_CASE_FORBIDDEN_TOOLS")

  if [[ -n "$AGENT_CASE_EXPECTED_TOOL_SEQUENCE" ]]; then
    actual_tool_sequence="$(jq -r '[.[] | select(.type=="tool.completed" or .type=="tool.failed") | .data.name] | join(",")' "$events_json")"
    if ! grep -Fxq "$actual_tool_sequence" < <(tr ';' '\n' <<<"$AGENT_CASE_EXPECTED_TOOL_SEQUENCE"); then
      echo "Agent run $run_id used unexpected tool sequence: got=$actual_tool_sequence want=$AGENT_CASE_EXPECTED_TOOL_SEQUENCE" >&2
      jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") | {sequence, name:.data.name, status:.data.status, input:.data.input}]' "$events_json" >&2
      exit 1
    fi
  fi

  if jq -e '
    [.[]
      | select(.type=="artifact.created" and .data.artifact.kind=="tool_call" and .data.artifact.data.name=="kube_insight_js")
      | .data.artifact.data.output
      | (if type=="string" then (fromjson? // {}) else . end)
      | ..
      | objects
      | select(has("nodeLifecycle") or has("recent_lifecycle_events"))
      | (.nodeLifecycle // .recent_lifecycle_events // [])[]
      | select(((.instance_type // .instanceType // "") | tostring) == "")
    ] | length > 0
  ' "$events_json" >/dev/null; then
    echo "Agent run $run_id returned Node lifecycle rows without instance type." >&2
    jq '[.[] | select(.type=="artifact.created" and .data.artifact.kind=="tool_call" and .data.artifact.data.name=="kube_insight_js") | .data.artifact.data.output]' "$events_json" >&2
    exit 1
  fi

  if [[ -n "$AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT" ]]; then
    if jq -e --arg fragment "$AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT" '
      [.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_health")]
      | sort_by(.sequence)
      | first
      | ((.data.input // {}) | tostring | contains($fragment))
    ' "$events_json" >/dev/null; then
      echo "Agent run $run_id passed forbidden fragment to the first kube_insight_health call: $AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT" >&2
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_health") | {sequence, input:.data.input}]' "$events_json" >&2
      exit 1
    fi
  fi

  if [[ -z "$final_answer" ]]; then
    echo "Agent run $run_id produced an empty final answer." >&2
    exit 1
  fi
  while IFS= read -r term; do
    if ! grep -Fqi "$term" <<<"$final_answer"; then
      echo "Agent run $run_id final answer is missing required term: $term" >&2
      printf '%s\n' "$final_answer" >&2
      exit 1
    fi
  done < <(csv_each "$AGENT_CASE_REQUIRED_ANSWER_TERMS")
  while IFS= read -r term; do
    if grep -Fqi "$term" <<<"$final_answer"; then
      echo "Agent run $run_id final answer contains forbidden term: $term" >&2
      printf '%s\n' "$final_answer" >&2
      exit 1
    fi
  done < <(csv_each "$AGENT_CASE_FORBIDDEN_ANSWER_TERMS")
  if [[ -n "$AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS" ]]; then
    local matched_any_term=0
    while IFS= read -r term; do
      if grep -Fqi "$term" <<<"$final_answer"; then
        matched_any_term=1
      fi
    done < <(pipe_each "$AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS")
    if [[ "$matched_any_term" != "1" ]]; then
      echo "Agent run $run_id final answer is missing any required timezone/local-time term: $AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS" >&2
      printf '%s\n' "$final_answer" >&2
      exit 1
    fi
  fi

  jq -n \
    --arg sessionId "$session_id" \
    --arg runId "$run_id" \
    --arg question "$AGENT_CASE_QUESTION" \
    --arg status "$status" \
    --arg eventsPath "$events_json" \
    --arg finalAnswer "$final_answer" \
    --argjson toolNames "$(jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed") | .data.name] | map(select(. != null))' "$events_json")" \
    '{sessionId:$sessionId, runId:$runId, question:$question, status:$status, toolNames:$toolNames, eventsPath:$eventsPath, finalAnswer:$finalAnswer}' \
    >"$OUT_DIR/$slug.summary.json"
  printf '%s: run=%s tools=%s\n' "$OUT_DIR/$slug.summary.json" "$run_id" "$(jq -r '.toolNames | join(",")' "$OUT_DIR/$slug.summary.json")"
}

request_json GET /healthz > "$OUT_DIR/healthz.json"
request_json GET /api/v1/server/info > "$OUT_DIR/server-info.json"
request_json GET /api/v1/schema > "$OUT_DIR/schema.json"
request_json GET '/api/v1/health?detail=full&limit=50' > "$OUT_DIR/resource-health.json"

sql_case clusters "select cluster_id, count() as fact_rows, max(ts) as last_fact_ts from facts group by cluster_id order by fact_rows desc limit 25"
sql_case oom_recent "select cluster_id, namespace, name, count() as oom_rows, max(ts) as last_seen from facts where fact_value = 'OOMKilled' and ts >= now() - interval 7 day group by cluster_id, namespace, name order by oom_rows desc, last_seen desc limit 25"
sql_case semantic_profile "select fact_key, count() as rows, min(ts) as first_seen, max(ts) as last_seen from facts where ts >= now() - interval 7 day group by fact_key order by rows desc limit 25"
sql_case allocation_profile "select fact_key, count() as rows, max(ts) as last_seen from facts where ts >= now() - interval 30 day and (positionCaseInsensitive(fact_key, 'request') > 0 or positionCaseInsensitive(fact_key, 'limit') > 0 or positionCaseInsensitive(fact_key, 'resource') > 0) group by fact_key order by rows desc limit 25"
sql_case allocation_doc_profile "select cluster_id, kind, observation_type, count() as rows, max(observed_at) as last_seen from observations where observed_at >= now() - interval 30 day and (position(doc, '\"requests\"') > 0 or position(doc, '\"limits\"') > 0) group by cluster_id, kind, observation_type order by rows desc, last_seen desc limit 25"
sql_case allocation_rollup "with latest_pods as (select cluster_id, namespace, name, uid, doc, row_number() over (partition by cluster_id, kind, namespace, name, uid order by observed_at desc) as rn from observations where kind = 'Pod' and observed_at >= now() - interval 1 day and position(doc, '\"resources\"') > 0), container_resources as (select cluster_id, namespace, name, arrayJoin(JSONExtractArrayRaw(doc, 'spec', 'containers')) as container_raw, JSONExtractString(container_raw, 'resources', 'requests', 'cpu') as cpu_request, JSONExtractString(container_raw, 'resources', 'requests', 'memory') as memory_request from latest_pods where rn = 1) select cluster_id, namespace, countDistinct(name) as pods, count() as containers, countIf(JSONExtractRaw(container_raw, 'resources', 'requests') != '') as containers_with_requests, countIf(JSONExtractRaw(container_raw, 'resources', 'limits') != '') as containers_with_limits, arrayStringConcat(arraySlice(groupUniqArrayIf(cpu_request, cpu_request != ''), 1, 5), ', ') as cpu_request_samples, arrayStringConcat(arraySlice(groupUniqArrayIf(memory_request, memory_request != ''), 1, 5), ', ') as memory_request_samples from container_resources group by cluster_id, namespace order by containers_with_requests desc, containers desc limit 25"
sql_case restart_by_namespace "select cluster_id, namespace, count() as restart_related_rows, max(ts) as last_seen from facts where ts >= now() - interval 7 day and (positionCaseInsensitive(fact_key, 'restart') > 0 or positionCaseInsensitive(fact_value, 'restart') > 0) group by cluster_id, namespace order by restart_related_rows desc, last_seen desc limit 25"

if [[ "$RUN_AGENT_CASE" == "1" || "$RUN_AGENT_CASE" == "true" ]]; then
  agent_case
fi

printf 'wrote ClickHouse agent case smoke output to %s\n' "$OUT_DIR"
