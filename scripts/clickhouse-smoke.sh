#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${CLICKHOUSE_IMAGE:-clickhouse/clickhouse-server:25.3}"
CONTAINER="${CLICKHOUSE_CONTAINER:-kube-insight-clickhouse-smoke}"
HTTP_PORT="${CLICKHOUSE_HTTP_PORT:-18123}"
ENDPOINT_BASE="http://127.0.0.1:${HTTP_PORT}"
DB="${CLICKHOUSE_DATABASE:-kube_insight}"
DRIVER_DB="${CLICKHOUSE_DRIVER_DATABASE:-${DB}_driver}"
DRIVER_CONFIG="/tmp/kube-insight-clickhouse-driver.yaml"
USER="${CLICKHOUSE_USER:-default}"
PASSWORD="${CLICKHOUSE_PASSWORD:-kube-insight}"
ENDPOINT="${ENDPOINT_BASE}/?user=${USER}&password=${PASSWORD}"
DISPLAY_ENDPOINT="${ENDPOINT_BASE}/?user=${USER}&password=***"
KEEP="${KEEP_CLICKHOUSE:-0}"

cleanup() {
  if [[ "${KEEP}" != "1" ]]; then
    docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
  fi
  rm -f "${DRIVER_CONFIG}"
}
trap cleanup EXIT

if ! command -v docker >/dev/null 2>&1; then
  echo "docker is required" >&2
  exit 1
fi

docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true

docker run -d \
  --name "${CONTAINER}" \
  -e CLICKHOUSE_DB="${DB}" \
  -e CLICKHOUSE_USER="${USER}" \
  -e CLICKHOUSE_PASSWORD="${PASSWORD}" \
  -e CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT=1 \
  -p "${HTTP_PORT}:8123" \
  "${IMAGE}" >/dev/null

for _ in $(seq 1 60); do
  if curl -fsS "${ENDPOINT_BASE}/ping" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -fsS "${ENDPOINT_BASE}/ping" >/dev/null 2>&1; then
  echo "ClickHouse did not become ready at ${ENDPOINT_BASE}" >&2
  docker logs "${CONTAINER}" >&2 || true
  exit 1
fi

if ! curl -fsS --data-binary "SELECT 1" "${ENDPOINT}" >/dev/null 2>&1; then
  echo "ClickHouse did not accept authenticated queries at ${ENDPOINT_BASE}" >&2
  docker logs "${CONTAINER}" >&2 || true
  exit 1
fi

BIN="${ROOT}/bin/kube-insight"
if [[ ! -x "${BIN}" ]]; then
  make -C "${ROOT}" build
fi

"${BIN}" db clickhouse init \
  --endpoint "${ENDPOINT}" \
  --database "${DB}" \
  --cold-after 0 >/tmp/kube-insight-clickhouse-init.json

offset_engine="$(curl -fsS --data-binary "SELECT engine FROM system.tables WHERE database = '${DB}' AND name = 'ingestion_offsets'" "${ENDPOINT}" | tr -d '[:space:]')"
if [[ "${offset_engine}" != "ReplacingMergeTree" ]]; then
  echo "ingestion_offsets engine = ${offset_engine}, want ReplacingMergeTree" >&2
  exit 1
fi

"${BIN}" db clickhouse import \
  --endpoint "${ENDPOINT}" \
  --database "${DB}" \
  --file "${ROOT}/testdata/fixtures/kube/core.json" \
  --output json >/tmp/kube-insight-clickhouse-import.json

"${BIN}" db clickhouse service default api \
  --endpoint "${ENDPOINT}" \
  --database "${DB}" >/tmp/kube-insight-clickhouse-service.json

if ! grep -Eq '"objects": [1-9][0-9]*' /tmp/kube-insight-clickhouse-service.json; then
  echo "service investigation did not return the expected object summary" >&2
  cat /tmp/kube-insight-clickhouse-service.json >&2
  exit 1
fi

if ! grep -Eq '"facts": [1-9][0-9]*' /tmp/kube-insight-clickhouse-service.json; then
  echo "service query did not return non-empty facts" >&2
  cat /tmp/kube-insight-clickhouse-service.json >&2
  exit 1
fi

if ! grep -Eq '"edges": [1-9][0-9]*' /tmp/kube-insight-clickhouse-service.json; then
  echo "service query did not return non-empty edges" >&2
  cat /tmp/kube-insight-clickhouse-service.json >&2
  exit 1
fi

cat >"${DRIVER_CONFIG}" <<YAML
version: v1alpha1
storage:
  driver: clickhouse
  clickhouse:
    initOnStart: true
    dsnEnv: KUBE_INSIGHT_CLICKHOUSE_DSN
    database: ${DRIVER_DB}
    coldAfterSeconds: 0
collection:
  enabled: false
YAML

KUBE_INSIGHT_CLICKHOUSE_DSN="${ENDPOINT}" "${BIN}" \
  --config "${DRIVER_CONFIG}" \
  ingest --file "${ROOT}/testdata/fixtures/kube/core.json" >/tmp/kube-insight-clickhouse-driver-ingest.json

"${BIN}" db clickhouse service default api \
  --endpoint "${ENDPOINT}" \
  --database "${DRIVER_DB}" >/tmp/kube-insight-clickhouse-driver-service.json

decision_rows="$(curl -fsS --data-binary "SELECT count() FROM \`${DRIVER_DB}\`.filter_decisions" "${ENDPOINT}" | tr -d '[:space:]')"
if [[ ! "${decision_rows}" =~ ^[0-9]+$ || "${decision_rows}" -le 0 ]]; then
  echo "driver=clickhouse did not persist auditable filter decisions" >&2
  echo "filter_decisions rows: ${decision_rows}" >&2
  exit 1
fi

if ! grep -Eq '"facts": [1-9][0-9]*' /tmp/kube-insight-clickhouse-driver-service.json; then
  echo "driver=clickhouse service query did not return non-empty facts" >&2
  cat /tmp/kube-insight-clickhouse-driver-service.json >&2
  exit 1
fi

if ! grep -Eq '"edges": [1-9][0-9]*' /tmp/kube-insight-clickhouse-driver-service.json; then
  echo "driver=clickhouse service query did not return non-empty edges" >&2
  cat /tmp/kube-insight-clickhouse-driver-service.json >&2
  exit 1
fi

if grep -Eq 'local/(pods|services|nodes|configmaps|secrets|serviceaccounts|persistentvolumeclaims)/' /tmp/kube-insight-clickhouse-driver-service.json; then
  echo "driver=clickhouse service query returned non-canonical edge aliases" >&2
  cat /tmp/kube-insight-clickhouse-driver-service.json >&2
  exit 1
fi

cat /tmp/kube-insight-clickhouse-service.json
cat /tmp/kube-insight-clickhouse-driver-service.json
