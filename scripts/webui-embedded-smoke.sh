#!/usr/bin/env bash
set -euo pipefail

BIN="${BIN:-bin/kube-insight}"
DB="${TMPDIR:-/tmp}/kube-insight-webui-smoke-$$.db"
API_LISTEN="${KUBE_INSIGHT_WEBUI_SMOKE_API_LISTEN:-127.0.0.1:18080}"
WEBUI_LISTEN="${KUBE_INSIGHT_WEBUI_SMOKE_WEBUI_LISTEN:-127.0.0.1:18081}"
LOG="${TMPDIR:-/tmp}/kube-insight-webui-smoke-$$.log"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
  rm -f "${DB}" "${DB}-shm" "${DB}-wal" "${LOG}"
}
trap cleanup EXIT

"${BIN}" serve \
  --api \
  --webui \
  --db "${DB}" \
  --api-listen "${API_LISTEN}" \
  --webui-listen "${WEBUI_LISTEN}" \
  >"${LOG}" 2>&1 &
SERVER_PID="$!"

for _ in $(seq 1 60); do
  if curl -fsS "http://${WEBUI_LISTEN}/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    cat "${LOG}" >&2 || true
    echo "kube-insight serve exited before web UI became ready" >&2
    exit 1
  fi
  sleep 0.25
done

page="$(curl -fsS "http://${WEBUI_LISTEN}/")"
case "${page}" in
  *'<div id="root"></div>'* | *'/assets/index-'*) ;;
  *)
    echo "embedded web UI index did not look like the React app shell" >&2
    echo "${page}" >&2
    exit 1
    ;;
esac

api_info="$(curl -fsS "http://${WEBUI_LISTEN}/api/v1/server/info")"
case "${api_info}" in
  *'"storage"'*'"components"'*) ;;
  *)
    echo "web UI API proxy did not return server info" >&2
    echo "${api_info}" >&2
    exit 1
    ;;
esac

echo "embedded web UI smoke passed at http://${WEBUI_LISTEN}"
