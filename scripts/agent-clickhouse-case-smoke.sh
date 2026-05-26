#!/usr/bin/env bash
set -euo pipefail

API_URL=${KUBE_INSIGHT_AGENT_CASE_API_URL:-http://127.0.0.1:8080}
OUT_DIR=${KUBE_INSIGHT_AGENT_CASE_OUTPUT:-testdata/generated/agent-clickhouse-case-smoke}
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

printf 'wrote ClickHouse agent case smoke output to %s\n' "$OUT_DIR"
