#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="${CHDB_SMOKE_OUTPUT:-${ROOT}/testdata/generated/chdb-smoke}"
CONFIG="${OUTPUT_DIR}/kube-insight.chdb-smoke.yaml"
DB_PATH="${CHDB_SMOKE_DB_PATH:-${OUTPUT_DIR}/db}"
DATABASE="${CHDB_SMOKE_DATABASE:-kube_insight}"
API_LISTEN="${CHDB_SMOKE_API_LISTEN:-127.0.0.1:18080}"
API_BASE="http://${API_LISTEN}"
BIN="${CHDB_SMOKE_BIN:-/tmp/kube-insight-chdb}"

find_chdb_lib() {
  if [[ -n "${CHDB_LIB_PATH:-}" ]]; then
    if [[ -r "${CHDB_LIB_PATH}" ]]; then
      printf '%s\n' "${CHDB_LIB_PATH}"
      return 0
    fi
    printf 'CHDB_LIB_PATH is set but not readable: %s\n' "${CHDB_LIB_PATH}" >&2
    return 1
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

  if command -v ldconfig >/dev/null 2>&1; then
    local found
    found="$(ldconfig -p 2>/dev/null | sed -n 's/.*=> //p' | grep '/libchdb\.so$' | head -n 1 || true)"
    if [[ -n "${found}" && -r "${found}" ]]; then
      printf '%s\n' "${found}"
      return 0
    fi
  fi

  return 1
}

lib_path="$(find_chdb_lib || true)"
if [[ -z "${lib_path}" ]]; then
  cat >&2 <<'MSG'
libchdb.so was not found, so chDB runtime smoke cannot run yet.

Install chdb-core/libchdb first, or set CHDB_LIB_PATH to the library path.
Reference: https://github.com/chdb-io/chdb-core#installation

This target does not auto-download runtime libraries.
MSG
  exit 2
fi
export CHDB_LIB_PATH="${lib_path}"

mkdir -p "${OUTPUT_DIR}"
rm -rf "${DB_PATH}"

(
  cd "${ROOT}"
  go build -tags chdb -o "${BIN}" ./cmd/kube-insight
)

cat >"${CONFIG}" <<YAML
version: v1alpha1
storage:
  driver: chdb
  chdb:
    path: ${DB_PATH}
    database: ${DATABASE}
collection:
  enabled: false
server:
  api:
    enabled: false
    listen: ${API_LISTEN}
YAML

"${BIN}" --config "${CONFIG}" config validate >/tmp/kube-insight-chdb-config.json
"${BIN}" --config "${CONFIG}" ingest --file "${ROOT}/testdata/fixtures/kube/core.json" >"${OUTPUT_DIR}/ingest.json"
"${BIN}" --config "${CONFIG}" db resources health --output json >"${OUTPUT_DIR}/health.json"
assert_file_contains() {
  local file="$1"
  local pattern="$2"
  local label="$3"
  if ! grep -q "${pattern}" "${file}"; then
    echo "${label} did not contain ${pattern}" >&2
    cat "${file}" >&2
    exit 1
  fi
}

"${BIN}" --config "${CONFIG}" query schema >"${OUTPUT_DIR}/cli-schema.json"
"${BIN}" --config "${CONFIG}" query sql   --sql "select kind, namespace, name from \`${DATABASE}\`.versions where name = 'api-0' limit 5"   >"${OUTPUT_DIR}/cli-sql.json"
"${BIN}" --config "${CONFIG}" query search api --limit 5 --include-health=false >"${OUTPUT_DIR}/cli-search.json"
"${BIN}" --config "${CONFIG}" query history --kind Pod --namespace default --name api-0 --max-versions 2 --max-observations 5 >"${OUTPUT_DIR}/cli-history.json"
"${BIN}" --config "${CONFIG}" query topology --kind Pod --namespace default --name api-0 >"${OUTPUT_DIR}/cli-topology.json"
"${BIN}" --config "${CONFIG}" query object --kind Pod --namespace default --name api-0 --max-versions-per-object 2 >"${OUTPUT_DIR}/cli-object.json"
"${BIN}" --config "${CONFIG}" query service api --namespace default --max-evidence-objects 20 >"${OUTPUT_DIR}/cli-service-investigation.json"

assert_file_contains "${OUTPUT_DIR}/cli-schema.json" '"name": "versions"' "CLI schema"
assert_file_contains "${OUTPUT_DIR}/cli-sql.json" 'api-0' "CLI SQL"
assert_file_contains "${OUTPUT_DIR}/cli-search.json" '"matches"' "CLI search"
assert_file_contains "${OUTPUT_DIR}/cli-history.json" '"versions"' "CLI history"
assert_file_contains "${OUTPUT_DIR}/cli-topology.json" 'pod_on_node' "CLI topology"
assert_file_contains "${OUTPUT_DIR}/cli-object.json" '"kind": "Pod"' "CLI object"
assert_file_contains "${OUTPUT_DIR}/cli-object.json" '"edges"' "CLI object"
assert_file_contains "${OUTPUT_DIR}/cli-service-investigation.json" '"facts"' "CLI service investigation"

"${BIN}" --config "${CONFIG}" serve --api --api-listen "${API_LISTEN}" >"${OUTPUT_DIR}/api.log" 2>&1 &
api_pid="$!"
cleanup() {
  kill "${api_pid}" >/dev/null 2>&1 || true
  wait "${api_pid}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

for _ in $(seq 1 60); do
  if curl -fsS "${API_BASE}/api/v1/health?limit=5" >"${OUTPUT_DIR}/api-health.json" 2>/dev/null; then
    break
  fi
  if ! kill -0 "${api_pid}" >/dev/null 2>&1; then
    echo "chDB API process exited early" >&2
    cat "${OUTPUT_DIR}/api.log" >&2 || true
    exit 1
  fi
  sleep 0.25
done

if ! curl -fsS "${API_BASE}/api/v1/health?limit=5" >"${OUTPUT_DIR}/api-health.json"; then
  echo "chDB API did not become ready at ${API_BASE}" >&2
  cat "${OUTPUT_DIR}/api.log" >&2 || true
  exit 1
fi

curl -fsS "${API_BASE}/api/v1/schema" >"${OUTPUT_DIR}/api-schema.json"
cat >"${OUTPUT_DIR}/api-sql-request.json" <<JSON
{"sql":"select kind, namespace, name from \`${DATABASE}\`.versions where name = 'api-0' limit 5","maxRows":5}
JSON
curl -fsS   -H 'Content-Type: application/json'   --data-binary "@${OUTPUT_DIR}/api-sql-request.json"   "${API_BASE}/api/v1/sql" >"${OUTPUT_DIR}/api-sql.json"
curl -fsS "${API_BASE}/api/v1/search?q=api&limit=5&includeHealth=false" >"${OUTPUT_DIR}/api-search.json"
curl -fsS "${API_BASE}/api/v1/history?kind=Pod&namespace=default&name=api-0&maxVersions=2&maxObservations=5" >"${OUTPUT_DIR}/api-history.json"
curl -fsS "${API_BASE}/api/v1/topology?kind=Pod&namespace=default&name=api-0" >"${OUTPUT_DIR}/api-topology.json"
curl -fsS "${API_BASE}/api/v1/services/default/api/investigation?limit=20" >"${OUTPUT_DIR}/api-service-investigation.json"

if ! grep -q '"name": "versions"' "${OUTPUT_DIR}/api-schema.json"; then
  echo "schema response did not include versions table" >&2
  cat "${OUTPUT_DIR}/api-schema.json" >&2
  exit 1
fi

if ! grep -q '"api-0"' "${OUTPUT_DIR}/api-sql.json"; then
  echo "SQL response did not include api-0" >&2
  cat "${OUTPUT_DIR}/api-sql.json" >&2
  exit 1
fi

if ! grep -Eq '"versions"[[:space:]]*:' "${OUTPUT_DIR}/api-history.json"; then
  echo "history response did not include versions" >&2
  cat "${OUTPUT_DIR}/api-history.json" >&2
  exit 1
fi

if ! grep -q 'pod_on_node' "${OUTPUT_DIR}/api-topology.json"; then
  echo "topology response did not include pod_on_node" >&2
  cat "${OUTPUT_DIR}/api-topology.json" >&2
  exit 1
fi

if ! grep -Eq '"facts"[[:space:]]*:' "${OUTPUT_DIR}/api-service-investigation.json"; then
  echo "service investigation response did not include facts" >&2
  cat "${OUTPUT_DIR}/api-service-investigation.json" >&2
  exit 1
fi

cat <<MSG
chDB smoke passed
library: ${CHDB_LIB_PATH}
database path: ${DB_PATH}
outputs: ${OUTPUT_DIR}
MSG
