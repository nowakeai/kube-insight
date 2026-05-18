#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${RELEASE_SMOKE_DIST:-${ROOT}/dist}"
OUTPUT_DIR="${RELEASE_SMOKE_OUTPUT:-${ROOT}/testdata/generated/release-artifact-smoke}"
FIXTURE="${RELEASE_SMOKE_FIXTURE:-${ROOT}/testdata/fixtures/kube/core.json}"
REQUIRE_CHDB="${RELEASE_SMOKE_REQUIRE_CHDB:-1}"

host_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "${host_os}" in
  linux*) host_os="linux" ;;
  darwin*) host_os="darwin" ;;
  msys*|mingw*|cygwin*) host_os="windows" ;;
esac
host_arch="$(uname -m)"
case "${host_arch}" in
  x86_64|amd64) host_arch="amd64" ;;
  aarch64|arm64) host_arch="arm64" ;;
  *) echo "unsupported host arch: ${host_arch}" >&2; exit 1 ;;
esac

mkdir -p "${OUTPUT_DIR}"
rm -rf "${OUTPUT_DIR:?}"/*

extract_archive() {
  local archive="$1"
  local dest="$2"
  mkdir -p "${dest}"
  case "${archive}" in
    *.tar.gz|*.tgz) tar -xzf "${archive}" -C "${dest}" ;;
    *.zip) unzip -q "${archive}" -d "${dest}" ;;
    *) echo "unsupported archive format: ${archive}" >&2; exit 1 ;;
  esac
}

find_archive() {
  local flavor="$1"
  if [[ "${flavor}" == "default" ]]; then
    find "${DIST_DIR}" -maxdepth 1 -type f \
      \( -name "kube-insight_*_${host_os}_${host_arch}.tar.gz" -o -name "kube-insight_*_${host_os}_${host_arch}.zip" \) \
      ! -name "*chdb*" | sort | tail -n 1
  else
    find "${DIST_DIR}" -maxdepth 1 -type f -name "kube-insight_*_chdb_${host_os}_${host_arch}.tar.gz" | sort | tail -n 1
  fi
}

write_sqlite_config() {
  local path="$1"
  local db="$2"
  cat >"${path}" <<YAML
version: v1alpha1
storage:
  driver: sqlite
  sqlite:
    path: ${db}
collection:
  enabled: false
YAML
}

write_chdb_config() {
  local path="$1"
  local dbpath="$2"
  cat >"${path}" <<YAML
version: v1alpha1
storage:
  driver: chdb
  chdb:
    path: ${dbpath}
    database: kube_insight
collection:
  enabled: false
YAML
}

smoke_default() {
  local archive="$1"
  local work="${OUTPUT_DIR}/default"
  extract_archive "${archive}" "${work}"
  local bin="${work}/kube-insight"
  if [[ "${host_os}" == "windows" ]]; then
    bin="${work}/kube-insight.exe"
  fi
  test -x "${bin}"
  write_sqlite_config "${work}/config.yaml" "${work}/kubeinsight.db"
  "${bin}" version >"${work}/version.txt"
  "${bin}" --config "${work}/config.yaml" config validate >"${work}/config-validate.json"
  "${bin}" --config "${work}/config.yaml" ingest --file "${FIXTURE}" >"${work}/ingest.json"
  "${bin}" --config "${work}/config.yaml" query schema >"${work}/schema.json"
  "${bin}" --config "${work}/config.yaml" query sql --sql "select ok.kind, li.namespace, li.name from latest_index li join object_kinds ok on ok.id = li.kind_id limit 5" >"${work}/sql.json"
  grep -q 'latest_index' "${work}/schema.json"
}

smoke_chdb() {
  local archive="$1"
  local work="${OUTPUT_DIR}/chdb"
  extract_archive "${archive}" "${work}"
  local bin="${work}/kube-insight"
  test -x "${bin}"
  test -r "${work}/libchdb.so"
  write_chdb_config "${work}/config.yaml" "${work}/db"
  CHDB_LIB_PATH="${work}/libchdb.so" "${bin}" version >"${work}/version.txt"
  CHDB_LIB_PATH="${work}/libchdb.so" "${bin}" --config "${work}/config.yaml" config validate >"${work}/config-validate.json"
  CHDB_LIB_PATH="${work}/libchdb.so" "${bin}" --config "${work}/config.yaml" ingest --file "${FIXTURE}" >"${work}/ingest.json"
  CHDB_LIB_PATH="${work}/libchdb.so" "${bin}" --config "${work}/config.yaml" query schema >"${work}/schema.json"
  CHDB_LIB_PATH="${work}/libchdb.so" "${bin}" --config "${work}/config.yaml" query sql --sql "select kind, namespace, name from \`kube_insight\`.versions limit 5" >"${work}/sql.json"
  grep -q 'versions' "${work}/schema.json"
  grep -q 'Pod' "${work}/sql.json"
}

default_archive="$(find_archive default || true)"
if [[ -z "${default_archive}" ]]; then
  echo "No default archive found in ${DIST_DIR} for ${host_os}/${host_arch}. Run goreleaser snapshot first." >&2
  exit 1
fi
smoke_default "${default_archive}"

chdb_archive="$(find_archive chdb || true)"
chdb_status="skipped"
if [[ -n "${chdb_archive}" ]]; then
  smoke_chdb "${chdb_archive}"
  chdb_status="passed"
elif [[ "${REQUIRE_CHDB}" == "1" && "${host_os}" != "windows" ]]; then
  echo "No chDB archive found in ${DIST_DIR} for ${host_os}/${host_arch}" >&2
  exit 1
fi

cat >"${OUTPUT_DIR}/summary.txt" <<EOF
Release artifact smoke passed
default_archive: ${default_archive}
chdb_archive: ${chdb_archive:-missing}
chdb_status: ${chdb_status}
outputs: ${OUTPUT_DIR}
EOF
cat "${OUTPUT_DIR}/summary.txt"
