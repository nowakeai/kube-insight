#!/usr/bin/env bash
set -euo pipefail

BIN="${BIN:-bin/kube-insight}"
DB="${TMPDIR:-/tmp}/kube-insight-webui-smoke-$$.db"
APP_LISTEN="${KUBE_INSIGHT_WEBUI_SMOKE_APP_LISTEN:-127.0.0.1:18090}"
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
  --app \
  --metrics \
  --db "${DB}" \
  --listen "${APP_LISTEN}" \
  >"${LOG}" 2>&1 &
SERVER_PID="$!"

for _ in $(seq 1 60); do
  if curl -fsS "http://${APP_LISTEN}/healthz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    cat "${LOG}" >&2 || true
    echo "kube-insight serve exited before web UI became ready" >&2
    exit 1
  fi
  sleep 0.25
done

page="$(curl -fsS "http://${APP_LISTEN}/")"
case "${page}" in
  *'<div id="root"></div>'* | *'/assets/index-'*) ;;
  *)
    echo "embedded web UI index did not look like the React app shell" >&2
    echo "${page}" >&2
    exit 1
    ;;
esac

api_info=""
for _ in $(seq 1 60); do
  if api_info="$(curl -fsS "http://${APP_LISTEN}/api/v1/server/info" 2>/dev/null)"; then
    break
  fi
  if ! kill -0 "${SERVER_PID}" >/dev/null 2>&1; then
    cat "${LOG}" >&2 || true
    echo "kube-insight serve exited before the embedded API became ready" >&2
    exit 1
  fi
  sleep 0.25
done

case "${api_info}" in
  *'"storage"'*'"components"'*) ;;
  *)
    echo "embedded API did not return server info" >&2
    echo "${api_info}" >&2
    exit 1
    ;;
esac

metrics_text="$(curl -fsS "http://${APP_LISTEN}/metrics")"
case "${metrics_text}" in
  *kube_insight*) ;;
  *)
    echo "embedded metrics endpoint did not return kube-insight metrics" >&2
    echo "${metrics_text}" >&2
    exit 1
    ;;
esac

echo "embedded web UI smoke passed at http://${APP_LISTEN}"
