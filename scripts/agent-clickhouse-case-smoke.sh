#!/usr/bin/env bash
set -euo pipefail

API_URL=${KUBE_INSIGHT_AGENT_CASE_API_URL:-http://127.0.0.1:8080}
OUT_DIR=${KUBE_INSIGHT_AGENT_CASE_OUTPUT:-testdata/generated/agent-clickhouse-case-smoke}
RUN_AGENT_CASE=${KUBE_INSIGHT_AGENT_CASE_RUN_AGENT:-0}
AGENT_CASE_SET=${KUBE_INSIGHT_AGENT_CASE_SET:-node-inventory}
AGENT_CASE_STRICT=${KUBE_INSIGHT_AGENT_CASE_STRICT:-1}
AGENT_CASE_SLUG=${KUBE_INSIGHT_AGENT_CASE_SLUG:-agent-custom}
AGENT_CASE_PARALLEL=${KUBE_INSIGHT_AGENT_CASE_PARALLEL:-1}
AGENT_CASE_QUESTION=${KUBE_INSIGHT_AGENT_CASE_QUESTION:-看看 gcp2半天内集群的节点是否有变化？一共有多少节点，有哪些类型？总的cpu 内存 量是多少}
AGENT_CASE_TIMEOUT=${KUBE_INSIGHT_AGENT_CASE_TIMEOUT_SECONDS:-360}
AGENT_CASE_REQUIRED_TOOLS=${KUBE_INSIGHT_AGENT_CASE_REQUIRED_TOOLS-kube_insight_health,kube_insight_schema,kube_insight_js}
AGENT_CASE_FORBIDDEN_TOOLS=${KUBE_INSIGHT_AGENT_CASE_FORBIDDEN_TOOLS-artifact_transform_js,kube_insight_scripted_query}
AGENT_CASE_EXPECTED_TOOL_SEQUENCE=${KUBE_INSIGHT_AGENT_CASE_EXPECTED_TOOL_SEQUENCE-}
AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT=${KUBE_INSIGHT_AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT-gcp2}
AGENT_CASE_REQUIRED_ANSWER_TERMS=${KUBE_INSIGHT_AGENT_CASE_REQUIRED_ANSWER_TERMS-节点,CPU,GiB}
AGENT_CASE_FORBIDDEN_ANSWER_TERMS=${KUBE_INSIGHT_AGENT_CASE_FORBIDDEN_ANSWER_TERMS-没有净增减}
AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS=${KUBE_INSIGHT_AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS-北京时间|Asia/Shanghai|+08:00}
AGENT_CASE_FORBIDDEN_JS_INPUT_TERMS=${KUBE_INSIGHT_AGENT_CASE_FORBIDDEN_JS_INPUT_TERMS-now()}
AGENT_CASE_FORBID_JS_ANTIPATTERNS=${KUBE_INSIGHT_AGENT_CASE_FORBID_JS_ANTIPATTERNS-1}
AGENT_CASE_REQUIRE_JS_ABSOLUTE_TIME=${KUBE_INSIGHT_AGENT_CASE_REQUIRE_JS_ABSOLUTE_TIME-1}
AGENT_CASE_NODE_LIFECYCLE_CHECK=${KUBE_INSIGHT_AGENT_CASE_NODE_LIFECYCLE_CHECK-1}
AGENT_CASE_REQUIRE_SCRATCH_HANDLES=${KUBE_INSIGHT_AGENT_CASE_REQUIRE_SCRATCH_HANDLES-0}
AGENT_CASE_MIN_CITATIONS=${KUBE_INSIGHT_AGENT_CASE_MIN_CITATIONS-0}
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

text_contains_term() {
  local text=$1
  local term=$2
  local alt
  while IFS= read -r alt; do
    if grep -Fqi "$alt" <<<"$text"; then
      return 0
    fi
  done < <(pipe_each "$term")
  return 1
}

agent_case() {
  local slug=$1
  local question=$2
  local required_tools=$3
  local forbidden_tools=$4
  local expected_tool_sequence=$5
  local forbidden_first_health_input=$6
  local required_answer_terms=$7
  local forbidden_answer_terms=$8
  local required_answer_any_terms=$9
  local node_lifecycle_instance_type_check=${10}
  local session_json session_id run_json run_id sse_path events_json status final_answer
  local failures=()
  local actual_tool_sequence=""

  printf 'running agent case %s: %s\n' "$slug" "$question" >&2
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
    --arg input "$question" \
    --argjson clientContext "$client_context_json" \
    '{input:$input, metadata:{clientContext:$clientContext}}')")"
  run_id="$(jq -r '.id' <<<"$run_json")"
  sse_path="$OUT_DIR/$slug.sse"
  events_json="$OUT_DIR/$slug.events.json"

  curl -fsS --max-time "$AGENT_CASE_TIMEOUT" "$API_URL/api/v1/agent/runs/$run_id/events?follow=true" >"$sse_path"
  awk '/^data: /{sub(/^data: /,""); print}' "$sse_path" | jq -s '.' >"$events_json"
  status="$(jq -r 'map(select(.type=="run.completed" or .type=="run.failed" or .type=="run.cancelled")) | last | .type // empty' "$events_json")"
  final_answer="$(jq -r '[.[] | select(.type=="answer.final" or .type=="message.completed") | .data.content // empty] | last // ""' "$events_json")"

  if [[ "$status" != "run.completed" ]]; then
    failures+=("run did not complete successfully: ${status:-missing-terminal-status}")
    jq 'map({type, sequence, data})' "$events_json" >&2
  fi

  if jq -e '[.[] | select(.type=="tool.failed" or .data.status=="failed")] | length > 0' "$events_json" >/dev/null; then
    failures+=("run has failed tool calls")
    jq '[.[] | select(.type=="tool.failed" or .data.status=="failed") | {type, sequence, data}]' "$events_json" >&2
  fi

  while IFS= read -r tool_name; do
    if ! jq -e --arg name "$tool_name" 'any(.[]; (.type=="tool.completed" or .type=="tool.audit") and .data.name==$name and (.data.status // "completed") != "failed")' "$events_json" >/dev/null; then
      failures+=("missing required tool: $tool_name")
      jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") | {sequence, name:.data.name, status:.data.status, input:.data.input}]' "$events_json" >&2
    fi
  done < <(csv_each "$required_tools")

  while IFS= read -r tool_name; do
    if jq -e --arg name "$tool_name" 'any(.[]; (.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") and .data.name==$name)' "$events_json" >/dev/null; then
      failures+=("called forbidden tool: $tool_name")
      jq --arg name "$tool_name" '[.[] | select((.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") and .data.name==$name) | {sequence, name:.data.name, status:.data.status, input:.data.input}]' "$events_json" >&2
    fi
  done < <(csv_each "$forbidden_tools")

  if [[ -n "$expected_tool_sequence" ]]; then
    actual_tool_sequence="$(jq -r '[.[] | select(.type=="tool.completed" or .type=="tool.failed") | .data.name] | join(",")' "$events_json")"
    if ! grep -Fxq "$actual_tool_sequence" < <(tr ';' '\n' <<<"$expected_tool_sequence"); then
      failures+=("unexpected tool sequence: got=$actual_tool_sequence want=$expected_tool_sequence")
      jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed" or .type=="tool.audit") | {sequence, name:.data.name, status:.data.status, input:.data.input}]' "$events_json" >&2
    fi
  fi

  if [[ "$node_lifecycle_instance_type_check" == "1" || "$node_lifecycle_instance_type_check" == "true" ]]; then
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
      failures+=("Node lifecycle rows missing instance type")
      jq '[.[] | select(.type=="artifact.created" and .data.artifact.kind=="tool_call" and .data.artifact.data.name=="kube_insight_js") | .data.artifact.data.output]' "$events_json" >&2
    fi
    if jq -e '
      [.[]
        | select(.type=="artifact.created" and .data.artifact.kind=="tool_call" and .data.artifact.data.name=="kube_insight_js")
        | .data.artifact.data.output
        | (if type=="string" then (fromjson? // {}) else . end)
        | ..
        | objects
        | select(has("op") or has("observation_type") or has("first_seen_utc") or has("first_seen"))
        | select(((.instance_type // .instanceType // "") | tostring | ascii_downcase) == "unknown"
                 or ((.instance_type // .instanceType // "") | tostring) == "")
      ] | length > 0
    ' "$events_json" >/dev/null; then
      failures+=("Node lifecycle rows still contain unknown/missing instance type after enrichment")
      jq '[.[] | select(.type=="artifact.created" and .data.artifact.kind=="tool_call" and .data.artifact.data.name=="kube_insight_js") | .data.artifact.data.output]' "$events_json" >&2
    fi
    if ! jq -e '
      any(.[]; .type=="artifact.created"
        and .data.artifact.kind=="tool_call"
        and .data.artifact.data.name=="kube_insight_js"
        and (
          (.data.artifact.data.output | tostring)
          | test("net_delta|netDelta|node_net_change|start_node_count|end_node_count|added_count|deleted_count"; "i")
        ))
    ' "$events_json" >/dev/null; then
      failures+=("Node change case missing explicit net/churn fields such as net_delta, added_count, deleted_count, or start/end node counts")
      jq '[.[] | select(.type=="artifact.created" and .data.artifact.kind=="tool_call" and .data.artifact.data.name=="kube_insight_js") | .data.artifact.data.output]' "$events_json" >&2
    fi
  fi

  if [[ "$slug" == "agent-pod-count-peak" ]]; then
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and (.data.name=="kube_insight_js" or .data.name=="kube_insight_sql")
        and (((.data.input.script // .data.input.sql // "") | tostring) | test("countDistinct\\s*\\([^)]*uid|count\\s*\\(\\s*distinct\\s+uid|count\\s*\\([^)]*uid|unique_pods"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("Pod-count peak tool input appears to count raw observation UIDs instead of reconstructing state")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and (.data.name=="kube_insight_js" or .data.name=="kube_insight_sql")) | {sequence, name:.data.name, input:.data.input}]' "$events_json" >&2
    fi
    if ! jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("BASELINE|MODIFIED|observation_type|state"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("Pod-count peak script missing state reconstruction markers such as baseline, MODIFIED, or observation_type")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if ! jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("pod_count_peak_intervals_for_js|interval_start|interval_end|from_baseline"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("Pod-count peak script did not use per-UID interval fields from pod_count_peak_intervals_for_js")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (
          (((.data.input.script // "") | tostring | ascii_downcase) | contains("name: '\''baseline'\''"))
          and (((.data.input.script // "") | tostring | ascii_downcase) | contains("name: '\''window_events'\''"))
          and (((.data.input.script // "") | tostring | ascii_downcase) | contains("sqlall"))
        ))
    ' "$events_json" >/dev/null; then
      failures+=("Pod-count peak script split the interval recipe into baseline/window_events queries")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (
          (((.data.input.script // "") | tostring | ascii_downcase) | contains("from observations"))
          and (
            (((.data.input.script // "") | tostring | ascii_downcase) | contains("uid, observation_type, observed_at"))
            or (((.data.input.script // "") | tostring | ascii_downcase) | contains("uid, observed_at, observation_type"))
          )
        ))
    ' "$events_json" >/dev/null; then
      failures+=("Pod-count peak script exports raw Pod observation rows instead of per-UID interval rows")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("totalCountHistory\\s*\\.\\s*filter|bucketPoints\\s*=\\s*totalCountHistory\\.filter|for\\s*\\([^)]*bucket[^)]*\\)[\\s\\S]{0,600}\\.filter\\("; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("Pod-count peak script uses per-bucket filtering over history and may timeout")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
  fi

  if [[ "$slug" == *"endpoint"* || "$slug" == *"service-empty"* ]]; then
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("arrayJoin\\s*\\(\\s*JSONExtractArrayRaw\\s*\\([^)]*endpoints"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("EndpointSlice readiness script uses arrayJoin over endpoints and may drop empty endpoint arrays")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
  fi

  if [[ -n "$forbidden_first_health_input" ]]; then
    if jq -e --arg fragment "$forbidden_first_health_input" '
      [.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_health")]
      | sort_by(.sequence)
      | first
      | ((.data.input // {}) | tostring | contains($fragment))
    ' "$events_json" >/dev/null; then
      failures+=("first kube_insight_health input contains forbidden fragment: $forbidden_first_health_input")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_health") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
  fi

  if [[ -n "$required_answer_any_terms" && -n "$AGENT_CASE_FORBIDDEN_JS_INPUT_TERMS" ]]; then
    while IFS= read -r term; do
      if jq -e --arg term "$term" '
        any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js" and ((.data.input // {}) | tostring | contains($term)))
      ' "$events_json" >/dev/null; then
        failures+=("JS input used forbidden relative/server-time term: $term")
        jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
      fi
    done < <(csv_each "$AGENT_CASE_FORBIDDEN_JS_INPUT_TERMS")
  fi

  if [[ "$AGENT_CASE_FORBID_JS_ANTIPATTERNS" == "1" || "$AGENT_CASE_FORBID_JS_ANTIPATTERNS" == "true" ]]; then
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("const\\s+sql\\s*=")))
    ' "$events_json" >/dev/null; then
      failures+=("JS script shadows sql helper with const sql")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("history\\s*:\\s*history|rows\\s*:\\s*rows\\s*[,}]")))
    ' "$events_json" >/dev/null; then
      failures+=("JS script returns broad raw rows/history instead of compact result or scratch handle")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("max\\s*\\(\\s*updated_at\\s*\\)\\s+as\\s+updated_at"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("JS SQL reuses updated_at as aggregate alias next to other aggregates")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input.script // "") | tostring) | test("(JSONExtractString|replace(All|RegexpAll)?)[^`]{0,240}/\\s*(1000|1024|1048576)"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("JS SQL appears to parse Kubernetes quantity strings inside ClickHouse; fetch raw strings and parse units in JS")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input // {}) | tostring) | test("\"maxRows\"\\s*:\\s*(50000|100000)|maxRows\\s*:\\s*(50000|100000)"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("JS script requested maxRows above the 10000 per-query cap")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
    if jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit")
        and .data.name=="kube_insight_js"
        and (((.data.input // {}) | tostring) | test("\"maxQueries\"\\s*:\\s*([1-9][1-9]|30)|maxQueries\\s*:\\s*([1-9][1-9]|30)"; "i")))
    ' "$events_json" >/dev/null; then
      failures+=("JS script requested maxQueries above the hard cap of 10")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
  fi

  if [[ -n "$required_answer_any_terms" && ( "$AGENT_CASE_REQUIRE_JS_ABSOLUTE_TIME" == "1" || "$AGENT_CASE_REQUIRE_JS_ABSOLUTE_TIME" == "true" ) ]]; then
    if jq -e 'any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js")' "$events_json" >/dev/null && ! jq -e '
      any(.[]; (.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js" and (((.data.input // {}) | tostring) | test("[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}")))
    ' "$events_json" >/dev/null; then
      failures+=("JS input missing absolute UTC time literal")
      jq '[.[] | select((.type=="tool.started" or .type=="tool.completed" or .type=="tool.audit") and .data.name=="kube_insight_js") | {sequence, input:.data.input}]' "$events_json" >&2
    fi
  fi

  if [[ -z "$final_answer" ]]; then
    failures+=("empty final answer")
  fi
  while IFS= read -r term; do
    if ! text_contains_term "$final_answer" "$term"; then
      failures+=("final answer missing required term: $term")
      printf '%s\n' "$final_answer" >&2
    fi
  done < <(csv_each "$required_answer_terms")
  while IFS= read -r term; do
    if grep -Fqi "$term" <<<"$final_answer"; then
      failures+=("final answer contains forbidden term: $term")
      printf '%s\n' "$final_answer" >&2
    fi
  done < <(csv_each "$forbidden_answer_terms")
  if [[ -n "$required_answer_any_terms" ]]; then
    local matched_any_term=0
    while IFS= read -r term; do
      if grep -Fqi "$term" <<<"$final_answer"; then
        matched_any_term=1
      fi
    done < <(pipe_each "$required_answer_any_terms")
    if [[ "$matched_any_term" != "1" ]]; then
      failures+=("final answer missing any required term group: $required_answer_any_terms")
      printf '%s\n' "$final_answer" >&2
    fi
  fi

  if [[ "$AGENT_CASE_REQUIRE_SCRATCH_HANDLES" == "1" || "$AGENT_CASE_REQUIRE_SCRATCH_HANDLES" == "true" ]]; then
    if ! jq -e '
      [
        .[]
        | select(.type=="artifact.created")
        | (.data.artifact.data.scratchHandles // [])[]
      ] | length > 0
    ' "$events_json" >/dev/null; then
      failures+=("missing required scratch handles")
      jq '[.[] | select(.type=="artifact.created" and .data.artifact.kind=="tool_call") | {sequence, name:.data.artifact.data.name, scratchHandles:.data.artifact.data.scratchHandles}]' "$events_json" >&2
    fi
  fi

  local failed_tool_calls artifact_count citation_count scratch_handle_count check_status check_failures_json
  failed_tool_calls="$(jq '[.[] | select(.type=="tool.failed" or .data.status=="failed")] | length' "$events_json")"
  artifact_count="$(jq '[.[] | select(.type=="artifact.created")] | length' "$events_json")"
  citation_count="$(jq '[.[] | select(.type=="citation.created")] | length' "$events_json")"
  scratch_handle_count="$(jq '[
    .[]
    | select(.type=="artifact.created")
    | (.data.artifact.data.scratchHandles // [])[]
  ] | length' "$events_json")"
  if (( citation_count < AGENT_CASE_MIN_CITATIONS )); then
    failures+=("citation count $citation_count is below required minimum $AGENT_CASE_MIN_CITATIONS")
    jq '[.[] | select(.type=="citation.created" or .type=="answer.final") | {sequence, type, data}]' "$events_json" >&2
  fi
  check_status="passed"
  if ((${#failures[@]} > 0)); then
    check_status="failed"
    check_failures_json="$(printf '%s\n' "${failures[@]}" | jq -R . | jq -s .)"
  else
    check_failures_json="[]"
  fi

  jq -n \
    --arg sessionId "$session_id" \
    --arg runId "$run_id" \
    --arg slug "$slug" \
    --arg question "$question" \
    --arg status "$status" \
    --arg checkStatus "$check_status" \
    --arg eventsPath "$events_json" \
    --arg finalAnswer "$final_answer" \
    --arg actualToolSequence "$actual_tool_sequence" \
    --argjson checkFailures "$check_failures_json" \
    --argjson toolNames "$(jq '[.[] | select(.type=="tool.completed" or .type=="tool.failed") | .data.name] | map(select(. != null))' "$events_json")" \
    --argjson failedToolCalls "$failed_tool_calls" \
    --argjson artifacts "$artifact_count" \
    --argjson citations "$citation_count" \
    --argjson scratchHandles "$scratch_handle_count" \
    '{sessionId:$sessionId, runId:$runId, slug:$slug, question:$question, status:$status, checkStatus:$checkStatus, checkFailures:$checkFailures, toolNames:$toolNames, failedToolCalls:$failedToolCalls, artifacts:$artifacts, citations:$citations, scratchHandles:$scratchHandles, actualToolSequence:$actualToolSequence, eventsPath:$eventsPath, finalAnswer:$finalAnswer}' \
    >"$OUT_DIR/$slug.summary.json"
  printf '%s: run=%s status=%s checks=%s tools=%s\n' "$OUT_DIR/$slug.summary.json" "$run_id" "$status" "$check_status" "$(jq -r '.toolNames | join(",")' "$OUT_DIR/$slug.summary.json")"
  if [[ "$check_status" != "passed" && "$AGENT_CASE_STRICT" != "0" && "$AGENT_CASE_STRICT" != "false" ]]; then
    printf 'Agent case %s failed checks:\n' "$slug" >&2
    printf ' - %s\n' "${failures[@]}" >&2
    exit 1
  fi
}

agent_case_configured() {
	agent_case \
		"agent-gcp2-half-day-node-capacity" \
    "$AGENT_CASE_QUESTION" \
    "$AGENT_CASE_REQUIRED_TOOLS" \
    "$AGENT_CASE_FORBIDDEN_TOOLS" \
    "$AGENT_CASE_EXPECTED_TOOL_SEQUENCE" \
    "$AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT" \
		"$AGENT_CASE_REQUIRED_ANSWER_TERMS" \
		"$AGENT_CASE_FORBIDDEN_ANSWER_TERMS" \
		"$AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS" \
		"1"
}

agent_case_custom() {
	agent_case \
		"$AGENT_CASE_SLUG" \
		"$AGENT_CASE_QUESTION" \
		"$AGENT_CASE_REQUIRED_TOOLS" \
		"$AGENT_CASE_FORBIDDEN_TOOLS" \
		"$AGENT_CASE_EXPECTED_TOOL_SEQUENCE" \
		"$AGENT_CASE_FORBIDDEN_FIRST_HEALTH_INPUT" \
		"$AGENT_CASE_REQUIRED_ANSWER_TERMS" \
		"$AGENT_CASE_FORBIDDEN_ANSWER_TERMS" \
		"$AGENT_CASE_REQUIRED_ANSWER_ANY_TERMS" \
		"$AGENT_CASE_NODE_LIFECYCLE_CHECK"
}

agent_aggregation_case_by_slug() {
	local slug=$1
	local required_tools="kube_insight_health,kube_insight_schema,kube_insight_js"
	local forbidden_tools="artifact_transform_js,kube_insight_scripted_query"
	local expected_sequence=""
  case "$slug" in
    agent-namespace-pod-resource-top)
      agent_case \
        "agent-namespace-pod-resource-top" \
        "帮我看看哪个namespace pod资源占用最高" \
        "$required_tools" \
        "$forbidden_tools" \
        "$expected_sequence" \
        "" \
        "namespace|Namespace|命名空间,Pod" \
        "" \
        "" \
        "0"
      ;;
    agent-namespace-resource-delta)
      agent_case \
        "agent-namespace-resource-delta" \
        "帮我看看过去几天哪个namespace资源占用有较大变化" \
        "$required_tools" \
        "$forbidden_tools" \
        "$expected_sequence" \
        "" \
        "namespace|Namespace|命名空间,变化" \
        "" \
        "北京时间|Asia/Shanghai|+08:00|UTC" \
        "0"
      ;;
    agent-pod-count-peak)
      agent_case \
        "agent-pod-count-peak" \
        "帮我看看过去一周pod数量最多的时间点是什么时候" \
        "$required_tools" \
        "$forbidden_tools" \
        "$expected_sequence" \
        "" \
        "Pod,时间" \
        "" \
        "北京时间|Asia/Shanghai|+08:00|UTC" \
        "0"
      ;;
    agent-pvc-resize-delta)
      agent_case \
        "agent-pvc-resize-delta" \
        "帮我看看过去一周有哪些 PVC 发生过扩容？从多少扩到多少？" \
        "$required_tools" \
        "$forbidden_tools" \
        "$expected_sequence" \
        "" \
        "PVC,GiB,扩" \
        "字节,bytes" \
        "北京时间|Asia/Shanghai|+08:00|UTC" \
        "0"
      ;;
    *)
      echo "unknown aggregation case slug: $slug" >&2
      return 1
      ;;
  esac
}

agent_aggregation_cases() {
  local slugs=(agent-namespace-pod-resource-top agent-namespace-resource-delta agent-pod-count-peak agent-pvc-resize-delta)
  if (( AGENT_CASE_PARALLEL > 1 )); then
    local pids=()
    local slug pid failures=0
    printf 'running aggregation agent cases in parallel: %s\n' "${slugs[*]}" >&2
    for slug in "${slugs[@]}"; do
      (
        agent_aggregation_case_by_slug "$slug"
      ) &
      pids+=("$!")
    done
    for pid in "${pids[@]}"; do
      if ! wait "$pid"; then
        failures=$((failures + 1))
      fi
    done
    if (( failures > 0 )); then
      echo "$failures parallel aggregation case(s) failed" >&2
      return 1
    fi
    return 0
  fi

  local slug
  for slug in "${slugs[@]}"; do
    agent_aggregation_case_by_slug "$slug"
  done
}

write_agent_case_report() {
  local summaries=("$OUT_DIR"/agent-*.summary.json)
  if [[ ! -e "${summaries[0]}" ]]; then
    return
  fi
  local generated_at report_json report_md
  generated_at="$(date -u +'%Y-%m-%dT%H:%M:%S.000Z')"
  report_json="$OUT_DIR/agent-cases-report.json"
  report_md="$OUT_DIR/agent-cases-report.md"
  jq -s \
    --arg generatedAt "$generated_at" \
    --arg caseSet "$AGENT_CASE_SET" \
    --arg strict "$AGENT_CASE_STRICT" '
      {
        generatedAt: $generatedAt,
        caseSet: $caseSet,
        strict: $strict,
        totals: {
          cases: length,
          passed: (map(select(.checkStatus == "passed")) | length),
          failed: (map(select(.checkStatus != "passed")) | length),
          completedRuns: (map(select(.status == "run.completed")) | length),
          toolCalls: ((map((.toolNames // []) | length) | add) // 0),
          failedToolCalls: ((map(.failedToolCalls // 0) | add) // 0),
          citations: ((map(.citations // 0) | add) // 0),
          scratchHandles: ((map(.scratchHandles // 0) | add) // 0)
        },
        cases: sort_by(.slug)
      }
    ' "${summaries[@]}" >"$report_json"
  jq -r '
    def cell: tostring | gsub("\\|"; "\\|") | gsub("[\\r\\n]+"; " ");
    [
      "# ClickHouse Agent Case Report",
      "",
      "- Generated: \(.generatedAt)",
      "- Case set: \(.caseSet)",
      "- Strict checks: \(.strict)",
      "",
      "| Metric | Value |",
      "| --- | ---: |",
      "| Cases | \(.totals.cases) |",
      "| Passed | \(.totals.passed) |",
      "| Failed | \(.totals.failed) |",
      "| Completed runs | \(.totals.completedRuns) |",
      "| Tool calls | \(.totals.toolCalls) |",
      "| Failed tool calls | \(.totals.failedToolCalls) |",
      "| Citations | \(.totals.citations) |",
      "| Scratch handles | \(.totals.scratchHandles) |",
      "",
      "| Case | Status | Checks | Tools | Citations | Scratch handles | Failures |",
      "| --- | --- | --- | --- | ---: | ---: | --- |"
    ][],
    (.cases[]? | "| \(.slug | cell) | \(.status | cell) | \(.checkStatus | cell) | \((.toolNames // []) | join(",") | cell) | \(.citations // 0) | \(.scratchHandles // 0) | \((.checkFailures // []) | join("; ") | cell) |")
  ' "$report_json" >"$report_md"
  printf 'agent case report: %s\n' "$report_md"
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
  case "$AGENT_CASE_SET" in
    node-inventory|default)
      agent_case_configured
      ;;
		aggregation)
			agent_aggregation_cases
			;;
		custom|single)
			agent_case_custom
			;;
		all)
			agent_case_configured
			agent_aggregation_cases
      ;;
		*)
			echo "unknown KUBE_INSIGHT_AGENT_CASE_SET: $AGENT_CASE_SET" >&2
			echo "valid values: node-inventory, aggregation, custom, all" >&2
			exit 1
			;;
  esac
  write_agent_case_report
fi

printf 'wrote ClickHouse agent case smoke output to %s\n' "$OUT_DIR"
