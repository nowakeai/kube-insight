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
OUTPUT_DIR="${CLICKHOUSE_LIVE_PROFILE_OUTPUT:-${ROOT}/testdata/generated/clickhouse-live-profile}"
API_BASE="${CLICKHOUSE_LIVE_PROFILE_API:-http://127.0.0.1:8080}"
TIMINGS_TSV="${OUTPUT_DIR}/timings.tsv"
WARNINGS_TXT="${OUTPUT_DIR}/warnings.txt"
SUMMARY_TXT="${OUTPUT_DIR}/summary.txt"
THRESHOLDS_TSV="${OUTPUT_DIR}/thresholds.tsv"
THRESHOLD_FAILURES_TXT="${OUTPUT_DIR}/threshold-failures.txt"
PREVIOUS_DIR=""
ENFORCE_THRESHOLDS="${CLICKHOUSE_LIVE_PROFILE_ENFORCE_THRESHOLDS:-1}"
MIN_COMPRESSION_RATIO="${CLICKHOUSE_LIVE_PROFILE_MIN_COMPRESSION_RATIO:-15}"
MAX_COMPRESSED_BYTES_PER_ROW="${CLICKHOUSE_LIVE_PROFILE_MAX_COMPRESSED_BYTES_PER_ROW:-100}"
MAX_API_MS="${CLICKHOUSE_LIVE_PROFILE_MAX_API_MS:-500}"
MAX_SERVICE_INVESTIGATION_MS="${CLICKHOUSE_LIVE_PROFILE_MAX_SERVICE_INVESTIGATION_MS:-1000}"

redacted_endpoint() {
  printf '%s\n' "${ENDPOINT}" | sed -E 's/(password=)[^&]*/\1***/g'
}

elapsed_ms() {
  local start="$1"
  local end
  end="$(date +%s%N)"
  awk -v s="${start}" -v e="${end}" 'BEGIN { printf "%.3f", (e - s) / 1000000 }'
}

float_lt() {
  awk -v left="$1" -v right="$2" 'BEGIN { exit !(left + 0 < right + 0) }'
}

float_gt() {
  awk -v left="$1" -v right="$2" 'BEGIN { exit !(left + 0 > right + 0) }'
}

record_threshold() {
  local signal="$1"
  local actual="$2"
  local operator="$3"
  local expected="$4"
  local status="$5"
  printf '%s	%s	%s	%s	%s
' "${signal}" "${actual}" "${operator}" "${expected}" "${status}" >>"${THRESHOLDS_TSV}"
  if [[ "${status}" != "pass" ]]; then
    printf '%s actual=%s expected=%s%s
' "${signal}" "${actual}" "${operator}" "${expected}" >>"${THRESHOLD_FAILURES_TXT}"
  fi
}

assert_min_threshold() {
  local signal="$1"
  local actual="$2"
  local expected="$3"
  if [[ -z "${actual}" ]]; then
    record_threshold "${signal}" "missing" ">=" "${expected}" "fail"
  elif float_lt "${actual}" "${expected}"; then
    record_threshold "${signal}" "${actual}" ">=" "${expected}" "fail"
  else
    record_threshold "${signal}" "${actual}" ">=" "${expected}" "pass"
  fi
}

assert_max_threshold() {
  local signal="$1"
  local actual="$2"
  local expected="$3"
  if [[ -z "${actual}" ]]; then
    record_threshold "${signal}" "missing" "<=" "${expected}" "fail"
  elif float_gt "${actual}" "${expected}"; then
    record_threshold "${signal}" "${actual}" "<=" "${expected}" "fail"
  else
    record_threshold "${signal}" "${actual}" "<=" "${expected}" "pass"
  fi
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

write_query() {
  local name="$1"
  local sql="$2"
  local format="$3"
  local file="$4"
  local start ms
  start="$(date +%s%N)"
  query "${sql}" "${format}" >"${file}"
  ms="$(elapsed_ms "${start}")"
  printf '%s\t%s\t%s\n' "sql" "${name}" "${ms}" >>"${TIMINGS_TSV}"
}

write_api() {
  local name="$1"
  local path="$2"
  local file="$3"
  local start ms status
  start="$(date +%s%N)"
  status="$(curl -sS -o "${file}" -w '%{http_code}' "${API_BASE}${path}" || true)"
  ms="$(elapsed_ms "${start}")"
  if [[ "${status}" =~ ^2 ]]; then
    printf '%s\t%s\t%s\n' "api" "${name}" "${ms}" >>"${TIMINGS_TSV}"
  else
    printf 'API query %s returned HTTP %s; response saved in %s\n' "${name}" "${status}" "${file}" >>"${WARNINGS_TXT}"
    printf '%s\t%s\t%s\n' "api_error" "${name}" "${ms}" >>"${TIMINGS_TSV}"
  fi
}

explain_query() {
  local name="$1"
  local sql="$2"
  local file="${OUTPUT_DIR}/explain-${name}.txt"
  if ! query "EXPLAIN indexes = 1 ${sql}" "TSVRaw" >"${file}" 2>"${file}.err"; then
    printf 'EXPLAIN indexes = 1 failed for %s; falling back to EXPLAIN. Error saved in %s.err\n' "${name}" "${file}" >>"${WARNINGS_TXT}"
    query "EXPLAIN ${sql}" "TSVRaw" >"${file}"
  fi
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

if [[ -d "${OUTPUT_DIR}" ]]; then
  PREVIOUS_DIR="$(mktemp -d)"
  for previous_file in storage-efficiency.tsv timings.tsv; do
    if [[ -f "${OUTPUT_DIR}/${previous_file}" ]]; then
      cp "${OUTPUT_DIR}/${previous_file}" "${PREVIOUS_DIR}/${previous_file}"
    fi
  done
fi
rm -rf "${OUTPUT_DIR}"
mkdir -p "${OUTPUT_DIR}"
printf 'kind\tname\telapsed_ms\n' >"${TIMINGS_TSV}"
: >"${WARNINGS_TXT}"
printf 'signal\tactual\toperator\texpected\tstatus\n' >"${THRESHOLDS_TSV}"
: >"${THRESHOLD_FAILURES_TXT}"

DBQ="\`${DB}\`"
DB_LITERAL="$(sql_quote "${DB}")"
BUSINESS_TABLES="'api_resources', 'observations', 'object_aliases', 'versions', 'facts', 'edges', 'changes', 'filter_decisions', 'ingestion_offsets'"

parts_sql="SELECT table, sum(rows) AS rows, sum(bytes_on_disk) AS bytes_on_disk, sum(data_compressed_bytes) AS compressed_bytes, sum(data_uncompressed_bytes) AS uncompressed_bytes, round(sum(data_uncompressed_bytes) / greatest(sum(data_compressed_bytes), 1), 2) AS compression_ratio, max(modification_time) AS latest_part FROM system.parts WHERE active AND database = ${DB_LITERAL} GROUP BY table ORDER BY table"
storage_efficiency_sql="SELECT total_rows AS rows, bytes_on_disk, compressed_bytes, uncompressed_bytes, round(uncompressed_bytes / greatest(compressed_bytes, 1), 2) AS compression_ratio, round(proof_compressed_bytes / greatest(compressed_bytes, 1) * 100, 2) AS proof_compressed_pct, round(derived_compressed_bytes / greatest(compressed_bytes, 1) * 100, 2) AS derived_compressed_pct, round(compressed_bytes / greatest(total_rows, 1), 2) AS compressed_bytes_per_row, formatReadableSize(bytes_on_disk) AS bytes_on_disk_readable, formatReadableSize(compressed_bytes) AS compressed_readable, formatReadableSize(uncompressed_bytes) AS uncompressed_readable FROM (SELECT sum(rows) AS total_rows, sum(bytes_on_disk) AS bytes_on_disk, sum(data_compressed_bytes) AS compressed_bytes, sum(data_uncompressed_bytes) AS uncompressed_bytes, sumIf(data_compressed_bytes, table IN ('observations', 'versions')) AS proof_compressed_bytes, sumIf(data_compressed_bytes, table IN ('facts', 'edges', 'changes')) AS derived_compressed_bytes FROM system.parts WHERE active AND database = ${DB_LITERAL} AND table IN (${BUSINESS_TABLES}))"
footprint_sql="SELECT database, state, parts, rows, bytes_on_disk, formatReadableSize(bytes_on_disk) AS bytes_readable, compressed_bytes, uncompressed_bytes, latest_modification, latest_remove FROM (SELECT database, if(active = 1, 'active', 'inactive') AS state, count() AS parts, sum(rows) AS rows, sum(bytes_on_disk) AS bytes_on_disk, sum(data_compressed_bytes) AS compressed_bytes, sum(data_uncompressed_bytes) AS uncompressed_bytes, max(modification_time) AS latest_modification, max(remove_time) AS latest_remove FROM system.parts WHERE database NOT IN ('INFORMATION_SCHEMA', 'information_schema') GROUP BY database, state) ORDER BY bytes_on_disk DESC"
inactive_parts_sql="SELECT database, table, parts, rows, bytes_on_disk, formatReadableSize(bytes_on_disk) AS bytes_readable, oldest_age_seconds, max_refcount, removal_states FROM (SELECT database, table, count() AS parts, sum(rows) AS rows, sum(bytes_on_disk) AS bytes_on_disk, max(dateDiff('second', remove_time, now())) AS oldest_age_seconds, max(refcount) AS max_refcount, groupArrayDistinct(removal_state) AS removal_states FROM system.parts WHERE active = 0 GROUP BY database, table) ORDER BY bytes_on_disk DESC LIMIT 30"
indexes_sql="SELECT table, name, type, expr FROM system.data_skipping_indices WHERE database = ${DB_LITERAL} ORDER BY table, name"
overview_sql="SELECT min(observed_at) AS first_observed, max(observed_at) AS latest_observed, count() AS observations, uniqExact(cluster_id) AS clusters, uniqExact(kind) AS kinds FROM ${DBQ}.observations"
offsets_sql="SELECT status, count() AS resources, max(latest_update) AS latest_update FROM (SELECT cluster_id, api_group, api_version, resource, namespace, argMax(status, updated_at) AS status, max(updated_at) AS latest_update FROM ${DBQ}.ingestion_offsets GROUP BY cluster_id, api_group, api_version, resource, namespace) GROUP BY status ORDER BY resources DESC"
kind_sql="SELECT cluster_id, kind, count() AS versions, uniqExact(namespace) AS namespaces, max(observed_at) AS latest FROM ${DBQ}.versions GROUP BY cluster_id, kind ORDER BY versions DESC, kind LIMIT 50"

write_query "parts" "${parts_sql}" "TSVWithNames" "${OUTPUT_DIR}/parts.tsv"
write_query "storage_efficiency" "${storage_efficiency_sql}" "TSVWithNames" "${OUTPUT_DIR}/storage-efficiency.tsv"
write_query "footprint" "${footprint_sql}" "TSVWithNames" "${OUTPUT_DIR}/footprint.tsv"
write_query "inactive_parts" "${inactive_parts_sql}" "TSVWithNames" "${OUTPUT_DIR}/inactive-parts.tsv"
write_query "data_skipping_indices" "${indexes_sql}" "TSVWithNames" "${OUTPUT_DIR}/data-skipping-indices.tsv"
write_query "overview" "${overview_sql}" "JSON" "${OUTPUT_DIR}/overview.json"
write_query "offset_status" "${offsets_sql}" "TSVWithNames" "${OUTPUT_DIR}/offset-status.tsv"
write_query "kind_distribution" "${kind_sql}" "TSVWithNames" "${OUTPUT_DIR}/kind-distribution.tsv"

pod_target="$(query "SELECT object_id, cluster_id, namespace, name FROM ${DBQ}.facts WHERE kind = 'Pod' AND severity >= 60 GROUP BY object_id, cluster_id, namespace, name ORDER BY max(ts) DESC, max(severity) DESC LIMIT 1" "TSV")"
if [[ -z "${pod_target}" ]]; then
  pod_target="$(query "SELECT object_id, cluster_id, namespace, name FROM ${DBQ}.versions WHERE kind = 'Pod' GROUP BY object_id, cluster_id, namespace, name ORDER BY max(observed_at) DESC LIMIT 1" "TSV")"
  printf 'No high-severity Pod fact found; using most recent Pod version.\n' >>"${WARNINGS_TXT}"
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

if [[ -n "${pod_object_id}" ]]; then
  pod_id_lit="$(sql_quote "${pod_object_id}")"
  pod_cluster_lit="$(sql_quote "${pod_cluster}")"
  pod_ns_lit="$(sql_quote "${pod_namespace}")"
  pod_name_lit="$(sql_quote "${pod_name}")"
  latest_version_sql="SELECT doc FROM ${DBQ}.versions WHERE object_id = ${pod_id_lit} ORDER BY observed_at DESC, seq DESC LIMIT 1"
  history_sql="SELECT seq, observed_at, resource_version, doc_hash, materialization, raw_size, stored_size FROM ${DBQ}.versions WHERE object_id = ${pod_id_lit} ORDER BY observed_at DESC, seq DESC LIMIT 20"
  facts_sql="SELECT ts, object_id, fact_key, fact_value, numeric_value, severity FROM ${DBQ}.facts WHERE object_id = ${pod_id_lit} ORDER BY ts DESC LIMIT 1000"
  changes_sql="SELECT ts, object_id, change_family, path, op, old_scalar, new_scalar, severity FROM ${DBQ}.changes WHERE object_id = ${pod_id_lit} ORDER BY ts DESC LIMIT 1000"
  topology_sql="SELECT edge_type, src_id, dst_id, argMax(src_kind, valid_from) AS src_kind, argMax(dst_kind, valid_from) AS dst_kind, min(valid_from) AS edge_valid_from, max(valid_to) AS edge_valid_to FROM ${DBQ}.edges WHERE src_id = ${pod_id_lit} OR dst_id = ${pod_id_lit} GROUP BY edge_type, src_id, dst_id ORDER BY edge_valid_from LIMIT 1000"
  search_facts_sql="SELECT object_id, fact_key AS reason, severity, ts FROM ${DBQ}.facts WHERE kind = 'Pod' AND positionCaseInsensitive(concat(fact_key, ' ', fact_value, ' ', detail), 'OOMKilled') > 0 ORDER BY ts DESC LIMIT 100"
  search_changes_sql="SELECT object_id, path AS reason, severity, ts FROM ${DBQ}.changes WHERE kind = 'Pod' AND positionCaseInsensitive(concat(change_family, ' ', path, ' ', old_scalar, ' ', new_scalar), 'OOMKilled') > 0 ORDER BY ts DESC LIMIT 100"

  write_query "versions_latest_lookup" "${latest_version_sql}" "JSON" "${OUTPUT_DIR}/versions-latest.json"
  write_query "object_history_versions" "${history_sql}" "JSON" "${OUTPUT_DIR}/history-versions.json"
  write_query "facts_by_object" "${facts_sql}" "JSON" "${OUTPUT_DIR}/facts-by-object.json"
  write_query "changes_by_object" "${changes_sql}" "JSON" "${OUTPUT_DIR}/changes-by-object.json"
  write_query "topology_edges" "${topology_sql}" "JSON" "${OUTPUT_DIR}/topology-edges.json"
  write_query "search_oomkilled_facts" "${search_facts_sql}" "JSON" "${OUTPUT_DIR}/search-oomkilled-facts.json"
  write_query "search_oomkilled_changes" "${search_changes_sql}" "JSON" "${OUTPUT_DIR}/search-oomkilled-changes.json"

  explain_query "versions-latest" "${latest_version_sql}"
  explain_query "facts-by-object" "${facts_sql}"
  explain_query "changes-by-object" "${changes_sql}"
  explain_query "topology-edges" "${topology_sql}"
  explain_query "search-oomkilled-facts" "${search_facts_sql}"
  explain_query "search-oomkilled-changes" "${search_changes_sql}"
else
  printf 'No Pod target found; skipped object-level SQL and API profile queries.\n' >>"${WARNINGS_TXT}"
fi

if curl -fsS "${API_BASE}/healthz" >/dev/null 2>&1; then
  write_api "health" "/api/v1/health?limit=20" "${OUTPUT_DIR}/api-health.json"
  if [[ -n "${pod_object_id}" ]]; then
    write_api "search" "/api/v1/search?q=OOMKilled&kind=Pod&limit=10&includeHealth=false" "${OUTPUT_DIR}/api-search.json"
    write_api "history" "/api/v1/history?cluster=$(urlencode "${pod_cluster}")&kind=Pod&namespace=$(urlencode "${pod_namespace}")&name=$(urlencode "${pod_name}")&maxVersions=20&maxObservations=50" "${OUTPUT_DIR}/api-history.json"
    write_api "topology" "/api/v1/topology?cluster=$(urlencode "${pod_cluster}")&kind=Pod&namespace=$(urlencode "${pod_namespace}")&name=$(urlencode "${pod_name}")" "${OUTPUT_DIR}/api-topology.json"
  fi
  if [[ -n "${service_name}" ]]; then
    write_api "service_investigation" "/api/v1/services/$(urlencode "${service_namespace}")/$(urlencode "${service_name}")/investigation?maxEvidenceObjects=20&maxVersionsPerObject=3" "${OUTPUT_DIR}/api-service-investigation.json"
  else
    printf 'No Service target found; skipped service investigation API query.\n' >>"${WARNINGS_TXT}"
  fi
else
  printf 'API %s is not reachable; skipped API profile queries.\n' "${API_BASE}" >>"${WARNINGS_TXT}"
fi

write_storage_trend() {
  local previous="$1"
  local current="$2"
  local output="$3"
  awk '
    BEGIN {
      FS = OFS = "\t"
      print "metric", "previous", "current", "delta", "delta_pct"
    }
    NR == FNR {
      if (FNR == 1) {
        for (i = 1; i <= NF; i++) {
          pidx[$i] = i
        }
        next
      }
      if (FNR == 2) {
        p["rows"] = $(pidx["rows"])
        p["bytes_on_disk"] = $(pidx["bytes_on_disk"])
        p["compressed_bytes"] = $(pidx["compressed_bytes"])
        p["uncompressed_bytes"] = $(pidx["uncompressed_bytes"])
        p["compression_ratio"] = $(pidx["compression_ratio"])
        p["proof_compressed_pct"] = $(pidx["proof_compressed_pct"])
        p["derived_compressed_pct"] = $(pidx["derived_compressed_pct"])
        p["compressed_bytes_per_row"] = $(pidx["compressed_bytes_per_row"])
      }
      next
    }
    FNR == 1 {
      for (i = 1; i <= NF; i++) {
        cidx[$i] = i
      }
      next
    }
    FNR == 2 {
      metrics[1] = "rows"
      metrics[2] = "bytes_on_disk"
      metrics[3] = "compressed_bytes"
      metrics[4] = "uncompressed_bytes"
      metrics[5] = "compression_ratio"
      metrics[6] = "proof_compressed_pct"
      metrics[7] = "derived_compressed_pct"
      metrics[8] = "compressed_bytes_per_row"
      for (i = 1; i <= 8; i++) {
        metric = metrics[i]
        current_value = $(cidx[metric])
        previous_value = p[metric]
        delta = current_value - previous_value
        if (previous_value + 0 == 0) {
          delta_pct = "n/a"
        } else {
          delta_pct = sprintf("%.2f", delta / previous_value * 100)
        }
        printf "%s\t%s\t%s\t%.4f\t%s\n", metric, previous_value, current_value, delta, delta_pct
      }
    }
  ' "${previous}" "${current}" >"${output}"
}

write_timing_trend() {
  local previous="$1"
  local current="$2"
  local output="$3"
  awk '
    BEGIN {
      FS = OFS = "\t"
      print "kind", "name", "previous_ms", "current_ms", "delta_ms", "delta_pct"
    }
    NR == FNR {
      if (FNR == 1) {
        next
      }
      prev[$1 "\t" $2] = $3
      next
    }
    FNR == 1 {
      next
    }
    {
      key = $1 "\t" $2
      if (!(key in prev)) {
        printf "%s\t%s\t%s\t%s\t%s\t%s\n", $1, $2, "n/a", $3, "n/a", "n/a"
        next
      }
      delta = $3 - prev[key]
      if (prev[key] + 0 == 0) {
        delta_pct = "n/a"
      } else {
        delta_pct = sprintf("%.2f", delta / prev[key] * 100)
      }
      printf "%s\t%s\t%s\t%s\t%.3f\t%s\n", $1, $2, prev[key], $3, delta, delta_pct
    }
  ' "${previous}" "${current}" >"${output}"
}

validate_thresholds() {
  local compression_ratio compressed_bytes_per_row
  compression_ratio="$(awk -F '	' 'NR == 1 { for (i = 1; i <= NF; i++) idx[$i] = i; next } NR == 2 { print $(idx["compression_ratio"]); exit }' "${OUTPUT_DIR}/storage-efficiency.tsv")"
  compressed_bytes_per_row="$(awk -F '	' 'NR == 1 { for (i = 1; i <= NF; i++) idx[$i] = i; next } NR == 2 { print $(idx["compressed_bytes_per_row"]); exit }' "${OUTPUT_DIR}/storage-efficiency.tsv")"
  assert_min_threshold "storage.compression_ratio" "${compression_ratio}" "${MIN_COMPRESSION_RATIO}"
  assert_max_threshold "storage.compressed_bytes_per_row" "${compressed_bytes_per_row}" "${MAX_COMPRESSED_BYTES_PER_ROW}"

  while IFS=$'	' read -r kind name elapsed; do
    if [[ "${kind}" == "kind" ]]; then
      continue
    fi
    if [[ "${kind}" == "api_error" ]]; then
      record_threshold "api.${name}.status" "error" "=" "2xx" "fail"
      continue
    fi
    if [[ "${kind}" != "api" ]]; then
      continue
    fi
    case "${name}" in
      health|search|history|topology)
        assert_max_threshold "api.${name}.elapsed_ms" "${elapsed}" "${MAX_API_MS}"
        ;;
      service_investigation|service-investigation)
        assert_max_threshold "api.${name}.elapsed_ms" "${elapsed}" "${MAX_SERVICE_INVESTIGATION_MS}"
        ;;
    esac
  done <"${TIMINGS_TSV}"
}

if [[ -n "${PREVIOUS_DIR}" ]]; then
  if [[ -f "${PREVIOUS_DIR}/storage-efficiency.tsv" ]]; then
    write_storage_trend "${PREVIOUS_DIR}/storage-efficiency.tsv" "${OUTPUT_DIR}/storage-efficiency.tsv" "${OUTPUT_DIR}/storage-efficiency-trend.tsv"
  fi
  if [[ -f "${PREVIOUS_DIR}/timings.tsv" ]]; then
    write_timing_trend "${PREVIOUS_DIR}/timings.tsv" "${TIMINGS_TSV}" "${OUTPUT_DIR}/timing-trend.tsv"
  fi
  rm -rf "${PREVIOUS_DIR}"
fi

if [[ "${ENFORCE_THRESHOLDS}" != "0" ]]; then
  validate_thresholds
fi

# Human-readable summary. Keep this deliberately compact and secret-free.
{
  printf 'ClickHouse live profile\n'
  printf 'endpoint: %s\n' "$(redacted_endpoint)"
  printf 'database: %s\n' "${DB}"
  printf 'output: %s\n' "${OUTPUT_DIR}"
  printf 'api: %s\n' "${API_BASE}"
  printf '\nSelected Pod target:\n'
  cat "${OUTPUT_DIR}/selected-pod.tsv"
  printf '\nSelected Service target:\n'
  cat "${OUTPUT_DIR}/selected-service.tsv"
  printf '\nTimings (ms):\n'
  column -t -s $'\t' "${TIMINGS_TSV}" 2>/dev/null || cat "${TIMINGS_TSV}"
  printf '\nStorage efficiency (active %s tables only):\n' "${DB}"
  column -t -s $'\t' "${OUTPUT_DIR}/storage-efficiency.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/storage-efficiency.tsv"
  printf '\nClickHouse footprint by database and part state:\n'
  column -t -s $'\t' "${OUTPUT_DIR}/footprint.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/footprint.tsv"
  printf '\nLargest inactive part groups:\n'
  column -t -s $'\t' "${OUTPUT_DIR}/inactive-parts.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/inactive-parts.tsv"
  if [[ -f "${OUTPUT_DIR}/storage-efficiency-trend.tsv" ]]; then
    printf '\nStorage efficiency trend vs previous run:\n'
    column -t -s $'\t' "${OUTPUT_DIR}/storage-efficiency-trend.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/storage-efficiency-trend.tsv"
  fi
  if [[ -f "${OUTPUT_DIR}/timing-trend.tsv" ]]; then
    printf '\nTiming trend vs previous run:\n'
    column -t -s $'\t' "${OUTPUT_DIR}/timing-trend.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/timing-trend.tsv"
  fi
  if [[ -s "${THRESHOLDS_TSV}" ]]; then
    printf '\nMVP threshold status:\n'
    column -t -s $'\t' "${THRESHOLDS_TSV}" 2>/dev/null || cat "${THRESHOLDS_TSV}"
  fi
  printf '\nRows and bytes by table:\n'
  column -t -s $'\t' "${OUTPUT_DIR}/parts.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/parts.tsv"
  printf '\nCurrent offset status:\n'
  column -t -s $'\t' "${OUTPUT_DIR}/offset-status.tsv" 2>/dev/null || cat "${OUTPUT_DIR}/offset-status.tsv"
  if [[ -s "${WARNINGS_TXT}" ]]; then
    printf '\nWarnings:\n'
    cat "${WARNINGS_TXT}"
  fi
} | tee "${SUMMARY_TXT}"

if [[ "${ENFORCE_THRESHOLDS}" != "0" && -s "${THRESHOLD_FAILURES_TXT}" ]]; then
  printf '\nMVP threshold failures:\n' >&2
  cat "${THRESHOLD_FAILURES_TXT}" >&2
  exit 1
fi
