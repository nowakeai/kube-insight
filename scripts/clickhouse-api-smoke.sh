#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
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

DB="${CLICKHOUSE_LIVE_DATABASE:-${CLICKHOUSE_DATABASE:-kube_insight}}"
API_BASE="${CLICKHOUSE_API_SMOKE_API:-http://127.0.0.1:8080}"
OUTPUT_DIR="${CLICKHOUSE_API_SMOKE_OUTPUT:-${ROOT}/testdata/generated/clickhouse-api-smoke}"
TIMINGS_TSV="${OUTPUT_DIR}/timings.tsv"
SUMMARY_TXT="${OUTPUT_DIR}/summary.txt"
WARNINGS_TXT="${OUTPUT_DIR}/warnings.txt"
THRESHOLDS_TSV="${OUTPUT_DIR}/thresholds.tsv"
MAX_API_MS="${CLICKHOUSE_API_SMOKE_MAX_API_MS:-500}"
MAX_SERVICE_INVESTIGATION_MS="${CLICKHOUSE_API_SMOKE_MAX_SERVICE_INVESTIGATION_MS:-1000}"

redacted_endpoint() {
  printf '%s\n' "${ENDPOINT}" | sed -E 's/(password=)[^&]*/\1***/g'
}

sql_quote() {
  local value="$1"
  value="${value//\'/\'\'}"
  printf "'%s'" "${value}"
}

query() {
  local sql="$1"
  local format="${2:-TSV}"
  curl -fsS --data-binary "${sql} FORMAT ${format}" "${ENDPOINT}"
}

urlencode() {
  local value="$1"
  local encoded=""
  local i char hex
  LC_ALL=C
  for ((i = 0; i < ${#value}; i++)); do
    char="${value:i:1}"
    case "${char}" in
      [a-zA-Z0-9.~_-]) encoded+="${char}" ;;
      *)
        printf -v hex '%%%02X' "'${char}"
        encoded+="${hex}"
        ;;
    esac
  done
  printf '%s' "${encoded}"
}

elapsed_ms() {
  local start="$1"
  local end
  end="$(date +%s%N)"
  awk -v s="${start}" -v e="${end}" 'BEGIN { printf "%.3f", (e - s) / 1000000 }'
}

float_gt() {
  awk -v left="$1" -v right="$2" 'BEGIN { exit !(left + 0 > right + 0) }'
}

max_ms_for_api() {
  case "$1" in
    service-investigation|service_investigation)
      printf '%s' "${MAX_SERVICE_INVESTIGATION_MS}"
      ;;
    *)
      printf '%s' "${MAX_API_MS}"
      ;;
  esac
}

assert_api_latency() {
  local name="$1"
  local elapsed="$2"
  local max_ms
  max_ms="$(max_ms_for_api "${name}")"
  if float_gt "${elapsed}" "${max_ms}"; then
    printf 'API smoke %s took %sms, expected <= %sms
' "${name}" "${elapsed}" "${max_ms}" >&2
    printf 'api.%s.elapsed_ms	%s	<=	%s	fail
' "${name}" "${elapsed}" "${max_ms}" >>"${THRESHOLDS_TSV}"
    return 1
  fi
  printf 'api.%s.elapsed_ms	%s	<=	%s	pass
' "${name}" "${elapsed}" "${max_ms}" >>"${THRESHOLDS_TSV}"
}

assert_contains() {
  local file="$1"
  local pattern="$2"
  local name="$3"
  if ! grep -q "${pattern}" "${file}"; then
    printf 'API smoke %s response did not contain pattern %s\n' "${name}" "${pattern}" >&2
    printf 'Response saved at %s\n' "${file}" >&2
    return 1
  fi
}

write_api() {
  local name="$1"
  local path="$2"
  local pattern="$3"
  local file="${OUTPUT_DIR}/api-${name}.json"
  local start status ms
  start="$(date +%s%N)"
  status="$(curl -sS -o "${file}" -w '%{http_code}' "${API_BASE}${path}" || true)"
  ms="$(elapsed_ms "${start}")"
  printf 'api\t%s\t%s\t%s\n' "${name}" "${status}" "${ms}" >>"${TIMINGS_TSV}"
  if [[ ! "${status}" =~ ^2 ]]; then
    printf 'API smoke %s returned HTTP %s\n' "${name}" "${status}" >&2
    printf 'Response saved at %s\n' "${file}" >&2
    return 1
  fi
  assert_contains "${file}" "${pattern}" "${name}"
  assert_api_latency "${name}" "${ms}"
}

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi
if [[ ! "${DB}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
  echo "CLICKHOUSE_LIVE_DATABASE must be a simple ClickHouse identifier" >&2
  exit 1
fi

PING_URL="${ENDPOINT%%\?*}"
PING_URL="${PING_URL%/}/ping"
if ! curl -fsS "${PING_URL}" >/dev/null 2>&1; then
  echo "ClickHouse is not reachable at $(redacted_endpoint)" >&2
  exit 1
fi
if ! curl -fsS --data-binary "SELECT 1" "${ENDPOINT}" >/dev/null 2>&1; then
  echo "ClickHouse rejected authenticated query at $(redacted_endpoint)" >&2
  exit 1
fi
if ! curl -fsS "${API_BASE}/healthz" >/dev/null 2>&1; then
  echo "kube-insight API is not reachable at ${API_BASE}" >&2
  exit 1
fi

rm -rf "${OUTPUT_DIR}"
mkdir -p "${OUTPUT_DIR}"
printf 'kind\tname\thttp_status\telapsed_ms\n' >"${TIMINGS_TSV}"
printf 'signal\tactual\toperator\texpected\tstatus\n' >"${THRESHOLDS_TSV}"
: >"${WARNINGS_TXT}"

DBQ="\`${DB}\`"
pod_target="$(query "SELECT object_id, cluster_id, namespace, name FROM ${DBQ}.facts WHERE kind = 'Pod' AND severity >= 60 GROUP BY object_id, cluster_id, namespace, name ORDER BY max(ts) DESC, max(severity) DESC LIMIT 1" "TSV")"
if [[ -z "${pod_target}" ]]; then
  pod_target="$(query "SELECT object_id, cluster_id, namespace, name FROM ${DBQ}.versions WHERE kind = 'Pod' GROUP BY object_id, cluster_id, namespace, name ORDER BY max(observed_at) DESC LIMIT 1" "TSV")"
  printf 'No high-severity Pod fact found; using latest Pod version.\n' >>"${WARNINGS_TXT}"
fi
service_target="$(query "SELECT namespace, name FROM ${DBQ}.versions WHERE kind = 'Service' GROUP BY namespace, name ORDER BY max(observed_at) DESC LIMIT 1" "TSV")"

pod_object_id=""
pod_cluster=""
pod_namespace=""
pod_name=""
if [[ -n "${pod_target}" ]]; then
  pod_object_id="$(awk 'BEGIN { FS="\t" } { print $1; exit }' <<<"${pod_target}")"
  pod_cluster="$(awk 'BEGIN { FS="\t" } { print $2; exit }' <<<"${pod_target}")"
  pod_namespace="$(awk 'BEGIN { FS="\t" } { print $3; exit }' <<<"${pod_target}")"
  pod_name="$(awk 'BEGIN { FS="\t" } { print $4; exit }' <<<"${pod_target}")"
fi
service_namespace=""
service_name=""
if [[ -n "${service_target}" ]]; then
  service_namespace="$(awk 'BEGIN { FS="\t" } { print $1; exit }' <<<"${service_target}")"
  service_name="$(awk 'BEGIN { FS="\t" } { print $2; exit }' <<<"${service_target}")"
fi

printf 'pod_object_id\tcluster\tnamespace\tname\n%s\t%s\t%s\t%s\n' "${pod_object_id}" "${pod_cluster}" "${pod_namespace}" "${pod_name}" >"${OUTPUT_DIR}/selected-pod.tsv"
printf 'service_namespace\tservice_name\n%s\t%s\n' "${service_namespace}" "${service_name}" >"${OUTPUT_DIR}/selected-service.tsv"

if [[ -z "${pod_object_id}" ]]; then
  echo "No Pod target found in ${DB}; cannot run history/topology API smoke" >&2
  exit 1
fi
if [[ -z "${service_name}" ]]; then
  echo "No Service target found in ${DB}; cannot run service investigation API smoke" >&2
  exit 1
fi

write_api "health" "/api/v1/health?limit=20" '"summary"'
write_api "search" "/api/v1/search?q=OOMKilled&kind=Pod&limit=10&includeHealth=false" '"matches"'
write_api "history" "/api/v1/history?cluster=$(urlencode "${pod_cluster}")&kind=Pod&namespace=$(urlencode "${pod_namespace}")&name=$(urlencode "${pod_name}")&maxVersions=20&maxObservations=50" '"versions"'
write_api "topology" "/api/v1/topology?cluster=$(urlencode "${pod_cluster}")&kind=Pod&namespace=$(urlencode "${pod_namespace}")&name=$(urlencode "${pod_name}")" '"nodes"'
write_api "service-investigation" "/api/v1/services/$(urlencode "${service_namespace}")/$(urlencode "${service_name}")/investigation?maxEvidenceObjects=20&maxVersionsPerObject=3" '"summary"'

{
  printf 'ClickHouse API smoke\n'
  printf 'endpoint: %s\n' "$(redacted_endpoint)"
  printf 'database: %s\n' "${DB}"
  printf 'api: %s\n' "${API_BASE}"
  printf 'output: %s\n' "${OUTPUT_DIR}"
  printf 'pod: %s/%s cluster=%s\n' "${pod_namespace}" "${pod_name}" "${pod_cluster}"
  printf 'service: %s/%s\n' "${service_namespace}" "${service_name}"
  printf 'status: ok\n'
  printf '\nMVP threshold status:\n'
  column -t -s $'\t' "${THRESHOLDS_TSV}" 2>/dev/null || cat "${THRESHOLDS_TSV}"
} >"${SUMMARY_TXT}"

cat "${SUMMARY_TXT}"
printf '\nTimings (ms):\n'
column -t -s $'\t' "${TIMINGS_TSV}" 2>/dev/null || cat "${TIMINGS_TSV}"
