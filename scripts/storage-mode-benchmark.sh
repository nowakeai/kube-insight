#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${KUBE_INSIGHT_BIN:-${ROOT}/bin/kube-insight}"
CHDB_BIN="${CHDB_BENCH_BIN:-${ROOT}/bin/kube-insight-chdb}"
FIXTURES="${STORAGE_BENCH_FIXTURES:-${ROOT}/testdata/fixtures/kube}"
OUTPUT_DIR="${STORAGE_BENCH_OUTPUT:-${ROOT}/testdata/generated/storage-mode-benchmark}"
DATASET_DIR="${OUTPUT_DIR}/dataset"
CLUSTERS="${STORAGE_BENCH_CLUSTERS:-2}"
COPIES="${STORAGE_BENCH_COPIES:-20}"
SQLITE_DB="${OUTPUT_DIR}/sqlite/kubeinsight.db"
CHDB_DB_PATH="${OUTPUT_DIR}/chdb/db"
CHDB_DATABASE="${CHDB_BENCH_DATABASE:-kube_insight}"
CLICKHOUSE_DATABASE="${CLICKHOUSE_BENCH_DATABASE:-kube_insight_bench_modes}"
CLICKHOUSE_ENDPOINT="${KUBE_INSIGHT_CLICKHOUSE_DSN:-${CLICKHOUSE_ENDPOINT:-}}"
CLICKHOUSE_HTTP_PORT="${CLICKHOUSE_HTTP_PORT:-8123}"
CLICKHOUSE_USER="${CLICKHOUSE_USER:-default}"
CLICKHOUSE_PASSWORD="${CLICKHOUSE_PASSWORD:-}"
INCLUDE_KUBECTL="${STORAGE_BENCH_INCLUDE_KUBECTL:-0}"
KUBECTL_CONTEXT="${STORAGE_BENCH_KUBECTL_CONTEXT:-}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
TARGET_NAMESPACE="${STORAGE_BENCH_NAMESPACE:-ns-01-0001}"
TARGET_SERVICE="${STORAGE_BENCH_SERVICE:-api-0001}"
TARGET_POD="${STORAGE_BENCH_POD:-api-0-0001}"

if [[ -z "${CLICKHOUSE_ENDPOINT}" ]]; then
  if [[ -z "${CLICKHOUSE_PASSWORD}" ]]; then
    CLICKHOUSE_ENDPOINT="http://127.0.0.1:${CLICKHOUSE_HTTP_PORT}/?user=${CLICKHOUSE_USER}"
  else
    CLICKHOUSE_ENDPOINT="http://127.0.0.1:${CLICKHOUSE_HTTP_PORT}/?user=${CLICKHOUSE_USER}&password=${CLICKHOUSE_PASSWORD}"
  fi
fi
CLICKHOUSE_ENDPOINT="${CLICKHOUSE_ENDPOINT%\"}"
CLICKHOUSE_ENDPOINT="${CLICKHOUSE_ENDPOINT#\"}"

redacted_clickhouse_endpoint() {
  printf '%s\n' "${CLICKHOUSE_ENDPOINT}" | sed -E 's/(password=)[^&]*/\1***/g'
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
  local start ms status
  start="$(date +%s%N)"
  if "$@" >"${output}" 2>"${output}.stderr"; then
    status="ok"
  else
    status="failed"
  fi
  ms="$(elapsed_ms "${start}")"
  printf '%s\t%s\t%s\t%s\t%s\n' "${backend}" "${operation}" "${status}" "${ms}" "${output}" | tee -a "${OUTPUT_DIR}/timings.tsv"
  if [[ "${status}" != "ok" ]]; then
    cat "${output}.stderr" >&2 || true
    return 1
  fi
}

write_config_sqlite() {
  local path="$1"
  cat >"${path}" <<YAML
version: v1alpha1
storage:
  driver: sqlite
  sqlite:
    path: ${SQLITE_DB}
collection:
  enabled: false
YAML
}

write_config_clickhouse() {
  local path="$1"
  cat >"${path}" <<YAML
version: v1alpha1
storage:
  driver: clickhouse
  clickhouse:
    dsnEnv: KUBE_INSIGHT_CLICKHOUSE_DSN
    database: ${CLICKHOUSE_DATABASE}
    initOnStart: true
    batchSize: 1000
    flushIntervalMillis: 1000
collection:
  enabled: false
YAML
}

write_config_chdb() {
  local path="$1"
  cat >"${path}" <<YAML
version: v1alpha1
storage:
  driver: chdb
  chdb:
    path: ${CHDB_DB_PATH}
    database: ${CHDB_DATABASE}
collection:
  enabled: false
YAML
}

sql_quote() {
  local value="$1"
  value="${value//\'/\'\'}"
  printf "'%s'" "${value}"
}

clickhouse_query_tsv() {
  local query="$1"
  curl -fsS --data-binary "${query} FORMAT TSV" "${CLICKHOUSE_ENDPOINT}"
}

find_chdb_lib() {
  if [[ -n "${CHDB_LIB_PATH:-}" && -r "${CHDB_LIB_PATH}" ]]; then
    printf '%s\n' "${CHDB_LIB_PATH}"
    return 0
  fi
  for candidate in \
    /usr/local/lib/libchdb.so \
    /usr/lib/libchdb.so \
    /usr/lib/x86_64-linux-gnu/libchdb.so \
    /opt/homebrew/lib/libchdb.dylib; do
    if [[ -r "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return 0
    fi
  done
  return 1
}

du_bytes() {
  local path="$1"
  if [[ ! -e "${path}" ]]; then
    printf '0'
    return
  fi
  du -sb "${path}" | awk '{print $1}'
}

sqlite_query() {
  "${BIN}" --db "${SQLITE_DB}" "$@"
}

clickhouse_query() {
  KUBE_INSIGHT_CLICKHOUSE_DSN="${CLICKHOUSE_ENDPOINT}" "${BIN}" --config "${OUTPUT_DIR}/clickhouse/config.yaml" "$@"
}

chdb_query() {
  CHDB_LIB_PATH="${CHDB_LIB_PATH}" "${CHDB_BIN}" --config "${OUTPUT_DIR}/chdb/config.yaml" "$@"
}

benchmark_backend_queries() {
  local backend="$1"
  local runner="$2"
  local dir="${OUTPUT_DIR}/${backend}"
  mkdir -p "${dir}"
  run_timed "${backend}" "resource-health" "${dir}/resource-health.json" "${runner}" db resources health --output json
  run_timed "${backend}" "search" "${dir}/search.json" "${runner}" query search api --limit 10 --include-health=false
  run_timed "${backend}" "history" "${dir}/history.json" "${runner}" query history --kind Pod --namespace "${TARGET_NAMESPACE}" --name "${TARGET_POD}" --max-versions 5 --max-observations 20
  run_timed "${backend}" "topology" "${dir}/topology.json" "${runner}" query topology --kind Pod --namespace "${TARGET_NAMESPACE}" --name "${TARGET_POD}"
  run_timed "${backend}" "service-investigation" "${dir}/service-investigation.json" "${runner}" query service "${TARGET_SERVICE}" --namespace "${TARGET_NAMESPACE}" --max-evidence-objects 20 --max-versions-per-object 2
}

sqlite_stats() {
  local out="${OUTPUT_DIR}/sqlite/storage.tsv"
  {
    printf 'metric\tvalue\n'
    printf 'database_bytes\t%s\n' "$(du_bytes "${SQLITE_DB}")"
    printf 'wal_bytes\t%s\n' "$(du_bytes "${SQLITE_DB}-wal")"
    printf 'shm_bytes\t%s\n' "$(du_bytes "${SQLITE_DB}-shm")"
  } >"${out}"
  sqlite_query query sql --output json --sql "
select 'latest_index' as table_name, count(*) as rows from latest_index
union all select 'versions', count(*) from versions
union all select 'object_facts', count(*) from object_facts
union all select 'object_edges', count(*) from object_edges
union all select 'object_changes', count(*) from object_changes
union all select 'object_observations', count(*) from object_observations
order by table_name" >"${OUTPUT_DIR}/sqlite/rows.json"
}

clickhouse_stats() {
  local out="${OUTPUT_DIR}/clickhouse/storage.tsv"
  clickhouse_query_tsv "SELECT table, sum(rows) AS rows, sum(data_compressed_bytes) AS compressed_bytes, sum(data_uncompressed_bytes) AS uncompressed_bytes, round(sum(data_uncompressed_bytes) / greatest(sum(data_compressed_bytes), 1), 2) AS compression_ratio FROM system.parts WHERE active AND database = $(sql_quote "${CLICKHOUSE_DATABASE}") GROUP BY table ORDER BY table" >"${out}"
}

chdb_stats() {
  local out="${OUTPUT_DIR}/chdb/storage.json"
  CHDB_LIB_PATH="${CHDB_LIB_PATH}" "${CHDB_BIN}" --config "${OUTPUT_DIR}/chdb/config.yaml" query sql --output json --sql "
select table, sum(rows) as rows, sum(data_compressed_bytes) as compressed_bytes, sum(data_uncompressed_bytes) as uncompressed_bytes,
       round(sum(data_uncompressed_bytes) / greatest(sum(data_compressed_bytes), 1), 2) as compression_ratio
from system.parts
where active and database = '${CHDB_DATABASE}'
group by table
order by table" >"${out}"
  {
    printf 'metric\tvalue\n'
    printf 'database_directory_bytes\t%s\n' "$(du_bytes "${CHDB_DB_PATH}")"
  } >"${OUTPUT_DIR}/chdb/footprint.tsv"
}

main() {
  rm -rf "${OUTPUT_DIR}"
  mkdir -p "${OUTPUT_DIR}/sqlite" "${OUTPUT_DIR}/clickhouse" "${OUTPUT_DIR}/chdb"
  printf 'backend\toperation\tstatus\telapsed_ms\toutput\n' >"${OUTPUT_DIR}/timings.tsv"

  if [[ ! -x "${BIN}" ]]; then
    make -C "${ROOT}" build >/dev/null
  fi

  local start
  start="$(date +%s%N)"
  "${BIN}" generate samples --fixtures "${FIXTURES}" --output "${DATASET_DIR}" --clusters "${CLUSTERS}" --copies "${COPIES}" >"${OUTPUT_DIR}/dataset-manifest.json"
  printf 'dataset\tgenerate\tok\t%s\t%s\n' "$(elapsed_ms "${start}")" "${OUTPUT_DIR}/dataset-manifest.json" | tee -a "${OUTPUT_DIR}/timings.tsv"

  write_config_sqlite "${OUTPUT_DIR}/sqlite/config.yaml"
  run_timed "sqlite" "ingest" "${OUTPUT_DIR}/sqlite/ingest.json" "${BIN}" --config "${OUTPUT_DIR}/sqlite/config.yaml" ingest --dir "${DATASET_DIR}" --db "${SQLITE_DB}"
  benchmark_backend_queries "sqlite" sqlite_query
  sqlite_stats

  local ping_url="${CLICKHOUSE_ENDPOINT%%\?*}"
  ping_url="${ping_url%/}/ping"
  if curl -fsS "${ping_url}" >/dev/null 2>&1 && curl -fsS --data-binary "SELECT 1" "${CLICKHOUSE_ENDPOINT}" >/dev/null 2>&1; then
    if [[ ! "${CLICKHOUSE_DATABASE}" =~ (^|_)bench($|_)|(^|_)modes($|_) ]]; then
      echo "Refusing to reset ClickHouse database ${CLICKHOUSE_DATABASE}; use a benchmark/modes database name" >&2
      exit 1
    fi
    curl -fsS --data-binary "DROP DATABASE IF EXISTS \`${CLICKHOUSE_DATABASE}\`" "${CLICKHOUSE_ENDPOINT}" >/dev/null
    write_config_clickhouse "${OUTPUT_DIR}/clickhouse/config.yaml"
    run_timed "clickhouse" "ingest" "${OUTPUT_DIR}/clickhouse/ingest.json" env KUBE_INSIGHT_CLICKHOUSE_DSN="${CLICKHOUSE_ENDPOINT}" "${BIN}" --config "${OUTPUT_DIR}/clickhouse/config.yaml" ingest --dir "${DATASET_DIR}"
    benchmark_backend_queries "clickhouse" clickhouse_query
    clickhouse_stats
  else
    printf 'clickhouse\tskip\tunavailable\t0\t%s\n' "$(redacted_clickhouse_endpoint)" | tee -a "${OUTPUT_DIR}/timings.tsv"
  fi

  local chdb_lib
  chdb_lib="$(find_chdb_lib || true)"
  if [[ -n "${chdb_lib}" ]]; then
    export CHDB_LIB_PATH="${chdb_lib}"
    if [[ ! -x "${CHDB_BIN}" ]]; then
      make -C "${ROOT}" build-chdb >/dev/null
    fi
    write_config_chdb "${OUTPUT_DIR}/chdb/config.yaml"
    run_timed "chdb" "ingest" "${OUTPUT_DIR}/chdb/ingest.json" env CHDB_LIB_PATH="${CHDB_LIB_PATH}" "${CHDB_BIN}" --config "${OUTPUT_DIR}/chdb/config.yaml" ingest --dir "${DATASET_DIR}"
    benchmark_backend_queries "chdb" chdb_query
    chdb_stats
  else
    printf 'chdb\tskip\tlibchdb-missing\t0\tset CHDB_LIB_PATH\n' | tee -a "${OUTPUT_DIR}/timings.tsv"
  fi

  if [[ "${INCLUDE_KUBECTL}" == "1" ]]; then
    local kubectl_args=()
    if [[ -n "${KUBECTL_CONTEXT}" ]]; then
      kubectl_args=(--context "${KUBECTL_CONTEXT}")
    fi
    run_timed "kubectl" "workload-inventory" "${OUTPUT_DIR}/kubectl-workload-inventory.txt" "${KUBECTL_BIN}" "${kubectl_args[@]}" get pods,services,endpointslices.discovery.k8s.io -A --request-timeout=20s -o name
  fi

  {
    printf 'Storage mode benchmark\n'
    printf 'output: %s\n' "${OUTPUT_DIR}"
    printf 'dataset: fixtures=%s clusters=%s copies=%s\n' "${FIXTURES}" "${CLUSTERS}" "${COPIES}"
    printf 'clickhouse: %s database=%s\n' "$(redacted_clickhouse_endpoint)" "${CLICKHOUSE_DATABASE}"
    printf '\nTimings:\n'
    column -t -s $'\t' "${OUTPUT_DIR}/timings.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/timings.tsv"
    printf '\nSQLite storage:\n'
    column -t -s $'\t' "${OUTPUT_DIR}/sqlite/storage.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/sqlite/storage.tsv"
    if [[ -f "${OUTPUT_DIR}/clickhouse/storage.tsv" ]]; then
      printf '\nClickHouse storage by table:\n'
      column -t -s $'\t' "${OUTPUT_DIR}/clickhouse/storage.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/clickhouse/storage.tsv"
    fi
    if [[ -f "${OUTPUT_DIR}/chdb/footprint.tsv" ]]; then
      printf '\nchDB footprint:\n'
      column -t -s $'\t' "${OUTPUT_DIR}/chdb/footprint.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/chdb/footprint.tsv"
    fi
  } | tee "${OUTPUT_DIR}/summary.txt"
}

main "$@"
