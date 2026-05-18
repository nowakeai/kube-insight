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
DB="${LIVE_SERVICE_DATABASE:-${CLICKHOUSE_DATABASE:-kube_insight}}"
API_BASE="${LIVE_SERVICE_API:-http://127.0.0.1:8080}"
OUTPUT_DIR="${LIVE_SERVICE_OUTPUT:-${ROOT}/testdata/generated/live-service-vs-kubectl}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
KUBECTL_CONTEXT="${LIVE_SERVICE_KUBECTL_CONTEXT:-${KUBECTL_CONTEXT:-}}"
NAMESPACE="${LIVE_SERVICE_NAMESPACE:-}"
SERVICE="${LIVE_SERVICE_NAME:-}"

redacted_endpoint() {
  printf '%s\n' "${ENDPOINT}" | sed -E 's/(password=)[^&]*/\1***/g'
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
      *) printf -v hex '%%%02X' "'${char}"; encoded+="${hex}" ;;
    esac
  done
  printf '%s' "${encoded}"
}

sql_quote() {
  local value="$1"
  value="${value//\'/\'\'}"
  printf "'%s'" "${value}"
}

query_tsv() {
  local sql="$1"
  curl -fsS --data-binary "${sql} FORMAT TSV" "${ENDPOINT}"
}

elapsed_ms() {
  local start="$1"
  local end
  end="$(date +%s%N)"
  awk -v s="${start}" -v e="${end}" 'BEGIN { printf "%.3f", (e - s) / 1000000 }'
}

run_timed() {
  local backend="$1"
  local operation="$2"
  local output="$3"
  shift 3
  local start status ms bytes
  start="$(date +%s%N)"
  if "$@" >"${output}" 2>"${output}.stderr"; then
    status="ok"
  else
    status="failed"
  fi
  ms="$(elapsed_ms "${start}")"
  bytes="$(wc -c <"${output}" | tr -d ' ')"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "${backend}" "${operation}" "${status}" "${ms}" "${bytes}" "${output}" | tee -a "${OUTPUT_DIR}/timings.tsv"
  if [[ "${status}" != "ok" ]]; then
    cat "${output}.stderr" >&2 || true
    return 1
  fi
}

mkdir -p "${OUTPUT_DIR}"
rm -f "${OUTPUT_DIR}"/* 2>/dev/null || true
printf 'backend\toperation\tstatus\telapsed_ms\tbytes\toutput\n' >"${OUTPUT_DIR}/timings.tsv"

ping_url="${ENDPOINT%%\?*}"
ping_url="${ping_url%/}/ping"
curl -fsS "${ping_url}" >/dev/null
curl -fsS "${API_BASE}/healthz" >/dev/null
command -v "${KUBECTL_BIN}" >/dev/null

kubectl_args=()
if [[ -n "${KUBECTL_CONTEXT}" ]]; then
  kubectl_args+=(--context "${KUBECTL_CONTEXT}")
fi

if [[ -z "${NAMESPACE}" || -z "${SERVICE}" ]]; then
  while read -r live_ns live_name; do
    [[ -n "${live_ns}" && -n "${live_name}" ]] || continue
    ns_candidate="$(sql_quote "${live_ns}")"
    name_candidate="$(sql_quote "${live_name}")"
    if query_tsv "SELECT 1 FROM \`${DB}\`.versions WHERE kind = 'Service' AND namespace = ${ns_candidate} AND name = ${name_candidate} LIMIT 1" | grep -q '1'; then
      NAMESPACE="${live_ns}"
      SERVICE="${live_name}"
      break
    fi
  done < <("${KUBECTL_BIN}" "${kubectl_args[@]}" get service -A --request-timeout=20s --no-headers -o custom-columns=NAMESPACE:.metadata.namespace,NAME:.metadata.name)
fi

if [[ -z "${NAMESPACE}" || -z "${SERVICE}" ]]; then
  target="$(query_tsv "SELECT namespace, name FROM \`${DB}\`.versions WHERE kind = 'Service' AND namespace != '' GROUP BY namespace, name ORDER BY max(observed_at) DESC LIMIT 1")"
  if [[ -z "${target}" ]]; then
    echo "No Service target found in ClickHouse database ${DB}" >&2
    exit 1
  fi
  NAMESPACE="$(awk 'BEGIN { FS="\t" } { print $1; exit }' <<<"${target}")"
  SERVICE="$(awk 'BEGIN { FS="\t" } { print $2; exit }' <<<"${target}")"
fi

ns_lit="$(sql_quote "${NAMESPACE}")"
svc_lit="$(sql_quote "${SERVICE}")"
service_sql="SELECT object_id, cluster_id, namespace, name, observed_at, resource_version FROM \`${DB}\`.versions WHERE kind = 'Service' AND namespace = ${ns_lit} AND name = ${svc_lit} ORDER BY observed_at DESC LIMIT 10"
edges_sql="SELECT edge_type, src_id, dst_id, valid_from, valid_to FROM \`${DB}\`.edges WHERE src_id IN (SELECT object_id FROM \`${DB}\`.versions WHERE kind = 'Service' AND namespace = ${ns_lit} AND name = ${svc_lit}) OR dst_id IN (SELECT object_id FROM \`${DB}\`.versions WHERE kind = 'Service' AND namespace = ${ns_lit} AND name = ${svc_lit}) ORDER BY valid_from DESC LIMIT 100"

run_timed kube-insight sql-service-versions "${OUTPUT_DIR}/insight-service-versions.tsv" query_tsv "${service_sql}"
run_timed kube-insight sql-service-edges "${OUTPUT_DIR}/insight-service-edges.tsv" query_tsv "${edges_sql}"
run_timed kube-insight api-service-investigation "${OUTPUT_DIR}/insight-service-investigation.json" \
  curl -fsS "${API_BASE}/api/v1/services/$(urlencode "${NAMESPACE}")/$(urlencode "${SERVICE}")/investigation?maxEvidenceObjects=20&maxVersionsPerObject=3"

run_timed kubectl get-service "${OUTPUT_DIR}/kubectl-service.json" \
  "${KUBECTL_BIN}" "${kubectl_args[@]}" get service -n "${NAMESPACE}" "${SERVICE}" --request-timeout=20s -o json
run_timed kubectl get-endpointslices "${OUTPUT_DIR}/kubectl-endpointslices.json" \
  "${KUBECTL_BIN}" "${kubectl_args[@]}" get endpointslices.discovery.k8s.io -n "${NAMESPACE}" -l "kubernetes.io/service-name=${SERVICE}" --request-timeout=20s -o json
run_timed kubectl list-pods-in-namespace "${OUTPUT_DIR}/kubectl-pods.json" \
  "${KUBECTL_BIN}" "${kubectl_args[@]}" get pods -n "${NAMESPACE}" --request-timeout=20s -o json
run_timed kubectl list-events-in-namespace "${OUTPUT_DIR}/kubectl-events.json" \
  "${KUBECTL_BIN}" "${kubectl_args[@]}" get events -n "${NAMESPACE}" --request-timeout=20s -o json

awk 'BEGIN { FS = OFS = "\t"; print "backend", "operations", "elapsed_ms", "bytes" } NR > 1 { count[$1]++; ms[$1] += $4; bytes[$1] += $5 } END { for (b in count) printf "%s\t%d\t%.3f\t%d\n", b, count[b], ms[b], bytes[b] }' "${OUTPUT_DIR}/timings.tsv" | sort >"${OUTPUT_DIR}/totals.tsv"

cat >"${OUTPUT_DIR}/summary.md" <<EOF
# Live Service vs kubectl

- endpoint: $(redacted_endpoint)
- api: ${API_BASE}
- database: ${DB}
- kubectl context: ${KUBECTL_CONTEXT:-current}
- target: ${NAMESPACE}/${SERVICE}

## Totals

\`\`\`tsv
$(cat "${OUTPUT_DIR}/totals.tsv")
\`\`\`

## Timings

\`\`\`tsv
$(cat "${OUTPUT_DIR}/timings.tsv")
\`\`\`
EOF
cat "${OUTPUT_DIR}/summary.md"
