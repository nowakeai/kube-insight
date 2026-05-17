#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT}/bin/kube-insight"
ENDPOINT="${KUBE_INSIGHT_CLICKHOUSE_DSN:-${CLICKHOUSE_ENDPOINT:-}}"
HTTP_PORT="${CLICKHOUSE_HTTP_PORT:-8123}"
USER="${CLICKHOUSE_USER:-default}"
PASSWORD="${CLICKHOUSE_PASSWORD:-}"
if [[ -z "${ENDPOINT}" ]]; then
  if [[ -z "${PASSWORD}" ]]; then
    ENDPOINT="http://127.0.0.1:${HTTP_PORT}/?user=${USER}"
  else
    ENDPOINT="http://127.0.0.1:${HTTP_PORT}/?user=${USER}&password=${PASSWORD}"
  fi
fi
ENDPOINT="${ENDPOINT%\"}"
ENDPOINT="${ENDPOINT#\"}"

DB="${CLICKHOUSE_BENCH_DATABASE:-kube_insight_bench}"
FIXTURES="${CLICKHOUSE_BENCH_FIXTURES:-${ROOT}/testdata/fixtures/kube}"
OUTPUT_DIR="${CLICKHOUSE_BENCH_OUTPUT:-${ROOT}/testdata/generated/clickhouse-benchmark}"
CLUSTERS="${CLICKHOUSE_BENCH_CLUSTERS:-2}"
COPIES="${CLICKHOUSE_BENCH_COPIES:-20}"
KEEP_DATA="${CLICKHOUSE_BENCH_KEEP_DATA:-1}"
RESET_DATA="${CLICKHOUSE_BENCH_RESET:-1}"
SERVICE_JSON="/tmp/kube-insight-clickhouse-benchmark-service.json"
IMPORT_JSON="/tmp/kube-insight-clickhouse-benchmark-import.json"
MANIFEST_JSON="/tmp/kube-insight-clickhouse-benchmark-manifest.json"
INIT_JSON="/tmp/kube-insight-clickhouse-benchmark-init.json"

redacted_endpoint() {
  printf '%s\n' "${ENDPOINT}" | sed -E 's/(password=)[^&]*/\1***/g'
}

elapsed_ms() {
  local start="$1"
  local end
  end="$(date +%s%N)"
  awk -v s="${start}" -v e="${end}" 'BEGIN { printf "%.3f", (e - s) / 1000000 }'
}

sql_quote() {
  local value="$1"
  value="${value//\'/\'\'}"
  printf "'%s'" "${value}"
}

query_tsv() {
  local query="$1"
  curl -fsS --data-binary "${query} FORMAT TSV" "${ENDPOINT}"
}

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi
if [[ ! -x "${BIN}" ]]; then
  make -C "${ROOT}" build >/dev/null
fi
PING_URL="${ENDPOINT%%\?*}"
PING_URL="${PING_URL%/}/ping"
if ! curl -fsS "${PING_URL}" >/dev/null 2>&1; then
  echo "ClickHouse is not reachable at $(redacted_endpoint)" >&2
  echo "Run: make clickhouse-up" >&2
  exit 1
fi
if ! curl -fsS --data-binary "SELECT 1" "${ENDPOINT}" >/dev/null 2>&1; then
  echo "ClickHouse rejected authenticated query at $(redacted_endpoint)" >&2
  exit 1
fi
if [[ ! "${DB}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  echo "CLICKHOUSE_BENCH_DATABASE must be a simple ClickHouse identifier" >&2
  exit 1
fi
if [[ "${RESET_DATA}" == "1" ]]; then
  if [[ ! "${DB}" =~ (^|_)bench($|_) ]]; then
    echo "Refusing to reset non-benchmark database ${DB}; set CLICKHOUSE_BENCH_RESET=0 or use a *_bench* database" >&2
    exit 1
  fi
  curl -fsS --data-binary "DROP DATABASE IF EXISTS \`${DB}\`" "${ENDPOINT}" >/dev/null
fi

rm -rf "${OUTPUT_DIR}"
mkdir -p "${OUTPUT_DIR}"

start="$(date +%s%N)"
"${BIN}" generate samples \
  --fixtures "${FIXTURES}" \
  --output "${OUTPUT_DIR}" \
  --clusters "${CLUSTERS}" \
  --copies "${COPIES}" >"${MANIFEST_JSON}"
generate_ms="$(elapsed_ms "${start}")"

start="$(date +%s%N)"
KUBE_INSIGHT_CLICKHOUSE_DSN="${ENDPOINT}" "${BIN}" db clickhouse init \
  --database "${DB}" \
  --cold-after 0 \
  --output json >"${INIT_JSON}"
init_ms="$(elapsed_ms "${start}")"

start="$(date +%s%N)"
KUBE_INSIGHT_CLICKHOUSE_DSN="${ENDPOINT}" "${BIN}" db clickhouse import \
  --database "${DB}" \
  --dir "${OUTPUT_DIR}" \
  --output json >"${IMPORT_JSON}"
import_ms="$(elapsed_ms "${start}")"

service_target="$(query_tsv "SELECT namespace, name FROM $(printf '`%s`' "${DB}").versions WHERE kind = 'Service' GROUP BY namespace, name ORDER BY namespace, name LIMIT 1")"
if [[ -z "${service_target}" ]]; then
  echo "benchmark import produced no Service rows" >&2
  exit 1
fi
service_namespace="$(awk 'BEGIN { FS="\t" } { print $1; exit }' <<<"${service_target}")"
service_name="$(awk 'BEGIN { FS="\t" } { print $2; exit }' <<<"${service_target}")"

start="$(date +%s%N)"
KUBE_INSIGHT_CLICKHOUSE_DSN="${ENDPOINT}" "${BIN}" db clickhouse service "${service_namespace}" "${service_name}" \
  --database "${DB}" >"${SERVICE_JSON}"
service_ms="$(elapsed_ms "${start}")"

rows_tsv="$(query_tsv "SELECT table, sum(rows) AS rows, sum(data_compressed_bytes) AS compressed_bytes, sum(data_uncompressed_bytes) AS uncompressed_bytes FROM system.parts WHERE active AND database = $(sql_quote "${DB}") GROUP BY table ORDER BY table")"
totals_tsv="$(query_tsv "SELECT sum(rows) AS rows, sum(data_compressed_bytes) AS compressed_bytes, sum(data_uncompressed_bytes) AS uncompressed_bytes FROM system.parts WHERE active AND database = $(sql_quote "${DB}")")"
indexes_tsv="$(query_tsv "SELECT table, name, type, expr FROM system.data_skipping_indices WHERE database = $(sql_quote "${DB}") ORDER BY table, name")"

printf 'ClickHouse benchmark\n'
printf 'endpoint: %s\n' "$(redacted_endpoint)"
printf 'database: %s\n' "${DB}"
printf 'dataset: clusters=%s copies=%s output=%s reset=%s keep=%s\n' "${CLUSTERS}" "${COPIES}" "${OUTPUT_DIR}" "${RESET_DATA}" "${KEEP_DATA}"
printf 'timing_ms: generate=%s init=%s import=%s service=%s\n' "${generate_ms}" "${init_ms}" "${import_ms}" "${service_ms}"
printf 'service_target: namespace=%s name=%s\n' "${service_namespace}" "${service_name}"
printf '\nRows and bytes by table:\n'
printf 'table\trows\tcompressed_bytes\tuncompressed_bytes\n'
printf '%s\n' "${rows_tsv}"
printf '\nTotal rows and bytes:\n'
printf 'rows\tcompressed_bytes\tuncompressed_bytes\n'
printf '%s\n' "${totals_tsv}"
printf '\nData skipping indexes:\n'
printf 'table\tname\ttype\texpr\n'
printf '%s\n' "${indexes_tsv}"

if ! grep -Eq '"facts": [1-9][0-9]*' "${SERVICE_JSON}"; then
  echo "service query did not return non-empty facts" >&2
  cat "${SERVICE_JSON}" >&2
  exit 1
fi
if ! grep -Eq '"edges": [1-9][0-9]*' "${SERVICE_JSON}"; then
  echo "service query did not return non-empty edges" >&2
  cat "${SERVICE_JSON}" >&2
  exit 1
fi

if [[ "${KEEP_DATA}" != "1" ]]; then
  curl -fsS --data-binary "DROP DATABASE IF EXISTS \`${DB}\`" "${ENDPOINT}" >/dev/null
fi
