#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${KUBE_INSIGHT_BIN:-${ROOT}/bin/kube-insight}"
OUTPUT_DIR="${MCP_SQL_FIRST_SMOKE_OUTPUT:-${ROOT}/testdata/generated/mcp-sql-first-smoke}"
CONFIG="${MCP_SQL_FIRST_SMOKE_CONFIG:-}"
DB_PATH="${MCP_SQL_FIRST_SMOKE_DB:-}"
MAX_ROWS="${MCP_SQL_FIRST_SMOKE_MAX_ROWS:-5}"

mkdir -p "${OUTPUT_DIR}"
rm -f "${OUTPUT_DIR}"/*.json "${OUTPUT_DIR}"/*.txt "${OUTPUT_DIR}"/*.jsonl 2>/dev/null || true

if [[ ! -x "${BIN}" ]]; then
  make -C "${ROOT}" build >/dev/null
fi

base_args=()
if [[ -n "${CONFIG}" ]]; then
  base_args+=(--config "${CONFIG}")
elif [[ -n "${DB_PATH}" ]]; then
  base_args+=(--db "${DB_PATH}")
else
  base_args+=(--config "${ROOT}/config/kube-insight.clickhouse.example.yaml")
fi

mcp_call() {
  local name="$1"
  local payload="$2"
  local output="$3"
  printf '%s\n' "${payload}" | "${BIN}" "${base_args[@]}" serve mcp >"${output}" 2>"${output}.stderr"
}

assert_contains() {
  local file="$1"
  local pattern="$2"
  local label="$3"
  if ! grep -q "${pattern}" "${file}"; then
    echo "${label} missing ${pattern}" >&2
    cat "${file}" >&2
    cat "${file}.stderr" >&2 2>/dev/null || true
    exit 1
  fi
}

mcp_call prompt \
  '{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"kube_insight_coverage_first","arguments":{"symptom":"pod restarts","cluster":"the active cluster"}}}' \
  "${OUTPUT_DIR}/prompt.jsonl"
assert_contains "${OUTPUT_DIR}/prompt.jsonl" 'kube_insight_schema' 'coverage prompt'
assert_contains "${OUTPUT_DIR}/prompt.jsonl" 'kube_insight_sql' 'coverage prompt'
assert_contains "${OUTPUT_DIR}/prompt.jsonl" 'argMax(status, updated_at)' 'coverage prompt'

mcp_call schema \
  '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kube_insight_schema","arguments":{}}}' \
  "${OUTPUT_DIR}/schema.jsonl"
assert_contains "${OUTPUT_DIR}/schema.jsonl" 'tables' 'schema response'
assert_contains "${OUTPUT_DIR}/schema.jsonl" 'Active SQL backend' 'schema response'

if grep -q 'ClickHouse-compatible\|"versions"\|versions' "${OUTPUT_DIR}/schema.jsonl"; then
  backend="clickhouse-compatible"
  target_sql="select cluster_id, kind, namespace, name from versions where kind = 'Pod' order by observed_at desc limit ${MAX_ROWS}"
  history_sql="select object_id, kind, namespace, name, observed_at from versions where kind = 'Pod' order by observed_at desc limit ${MAX_ROWS}"
elif grep -q 'object_facts\|latest_index' "${OUTPUT_DIR}/schema.jsonl"; then
  backend="sqlite"
  target_sql="select cluster_id, kind, namespace, name from latest_index where kind = 'Pod' order by updated_at desc limit ${MAX_ROWS}"
  history_sql="select object_id, kind, namespace, name, updated_at from latest_index where kind = 'Pod' order by updated_at desc limit ${MAX_ROWS}"
else
  echo "Unable to detect backend from MCP schema" >&2
  cat "${OUTPUT_DIR}/schema.jsonl" >&2
  exit 1
fi

python3 - "$target_sql" "${MAX_ROWS}" >"${OUTPUT_DIR}/sql-request.json" <<'PYJSON'
import json, sys
print(json.dumps({
    "jsonrpc": "2.0",
    "id": 3,
    "method": "tools/call",
    "params": {"name": "kube_insight_sql", "arguments": {"sql": sys.argv[1], "maxRows": int(sys.argv[2])}},
}))
PYJSON
mcp_call sql "$(cat "${OUTPUT_DIR}/sql-request.json")" "${OUTPUT_DIR}/sql.jsonl"
assert_contains "${OUTPUT_DIR}/sql.jsonl" 'rowCount' 'SQL response'
assert_contains "${OUTPUT_DIR}/sql.jsonl" 'Pod' 'SQL response'

python3 - "$history_sql" "${MAX_ROWS}" >"${OUTPUT_DIR}/proof-sql-request.json" <<'PYJSON'
import json, sys
print(json.dumps({
    "jsonrpc": "2.0",
    "id": 4,
    "method": "tools/call",
    "params": {"name": "kube_insight_sql", "arguments": {"sql": sys.argv[1], "maxRows": int(sys.argv[2])}},
}))
PYJSON
mcp_call proof_sql "$(cat "${OUTPUT_DIR}/proof-sql-request.json")" "${OUTPUT_DIR}/proof-sql.jsonl"
assert_contains "${OUTPUT_DIR}/proof-sql.jsonl" 'rowCount' 'proof SQL response'

cat >"${OUTPUT_DIR}/summary.txt" <<EOF
MCP SQL-first smoke passed
backend: ${backend}
base args: ${base_args[*]}
outputs: ${OUTPUT_DIR}
validated: prompt -> schema -> sql -> proof sql
EOF
cat "${OUTPUT_DIR}/summary.txt"
