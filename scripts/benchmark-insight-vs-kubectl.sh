#!/usr/bin/env bash
set -euo pipefail

db_path="${1:-kubeinsight.db}"
context="${2:-}"
out_dir="${3:-testdata/generated/insight-vs-kubectl-$(date -u +%Y%m%dT%H%M%SZ)}"
cluster_id="${INSIGHT_CLUSTER_ID:-1}"

kube_insight="${KUBE_INSIGHT_BIN:-./bin/kube-insight}"
kubectl_bin="${KUBECTL_BIN:-kubectl}"

if [ -n "${GCLOUD_SDK_BIN:-}" ]; then
  export PATH="${GCLOUD_SDK_BIN}:${PATH}"
elif [ -d "${HOME}/google-cloud-sdk/bin" ]; then
  export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"
fi

mkdir -p "${out_dir}"

if [[ ! "${cluster_id}" =~ ^[0-9]+$ ]]; then
  echo "INSIGHT_CLUSTER_ID must be a numeric clusters.id value" >&2
  exit 2
fi

kubectl_context_args=()
if [ -n "${context}" ]; then
  kubectl_context_args=(--context "${context}")
fi

run_timed() {
  local label="$1"
  shift
  local out_file="${out_dir}/${label}.txt"
  local start end duration_ms
  start="$(date +%s%3N)"
  "$@" > "${out_file}"
  end="$(date +%s%3N)"
  duration_ms="$((end - start))"
  printf '%s\t%sms\t%s\n' "${label}" "${duration_ms}" "${out_file}"
}

policy_sql="
select
  reason.fact_value as reason,
  count(*) as retained_events,
  min(datetime(reason.ts/1000,'unixepoch')) as first_seen,
  max(datetime(reason.ts/1000,'unixepoch')) as latest_seen
from object_facts reason
join object_facts typ
  on typ.version_id = reason.version_id
 and typ.fact_key = 'k8s_event.type'
where reason.cluster_id = ${cluster_id}
  and reason.fact_key <> 'k8s_event.message_preview'
  and reason.fact_key = 'k8s_event.reason'
  and reason.fact_value = 'PolicyViolation'
  and typ.fact_value = 'Warning'
group by reason.fact_value;
"

event_edges_sql="
select
  datetime(reason.ts/1000,'unixepoch') as event_time,
  reason.fact_value as reason,
  preview.fact_value as message_preview,
  dst_kind.kind as target_kind,
  coalesce(dst.namespace,'') as target_namespace,
  dst.name as target_name
from object_facts reason
join objects ev on ev.id = reason.object_id
join object_edges e
  on e.src_id = ev.id
 and e.edge_type in (
   'event_regarding_object',
   'event_related_object',
   'event_involves_object'
 )
join objects dst on dst.id = e.dst_id
join object_kinds dst_kind on dst_kind.id = dst.kind_id
left join object_facts preview
  on preview.version_id = reason.version_id
 and preview.fact_key = 'k8s_event.message_preview'
where reason.cluster_id = ${cluster_id}
  and reason.fact_key <> 'k8s_event.message_preview'
  and reason.fact_key = 'k8s_event.reason'
  and reason.fact_value in (
    'PolicyViolation',
    'FailedCreate',
    'FailedScheduling',
    'BackOff',
    'Unhealthy',
    'FailedMount'
  )
order by reason.ts desc
limit 20;
"

printf 'benchmark\telapsed\toutput\n' | tee "${out_dir}/summary.tsv"
run_timed "insight-policy-count" \
  "${kube_insight}" --db "${db_path}" query sql --output table --sql "${policy_sql}" | tee -a "${out_dir}/summary.tsv"

run_timed "insight-event-edges" \
  "${kube_insight}" --db "${db_path}" query sql --output table --max-rows 20 --sql "${event_edges_sql}" | tee -a "${out_dir}/summary.tsv"

run_timed "kubectl-warning-count" \
  bash -c 'kubectl_bin="$1"; shift; "$kubectl_bin" "$@" get events.events.k8s.io -A --field-selector type=Warning --request-timeout=20s -o name | wc -l' \
  bash "${kubectl_bin}" "${kubectl_context_args[@]}" | tee -a "${out_dir}/summary.tsv"

run_timed "kubectl-policy-count" \
  bash -c 'kubectl_bin="$1"; shift; "$kubectl_bin" "$@" get events.events.k8s.io -A --field-selector type=Warning,reason=PolicyViolation --request-timeout=20s -o name | wc -l' \
  bash "${kubectl_bin}" "${kubectl_context_args[@]}" | tee -a "${out_dir}/summary.tsv"

echo "Benchmark output: ${out_dir}"
