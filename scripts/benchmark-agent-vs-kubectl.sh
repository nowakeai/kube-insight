#!/usr/bin/env bash
set -euo pipefail

db_path="${1:-kubeinsight.db}"
context="${2:-}"
out_dir="${3:-testdata/generated/agent-vs-kubectl-$(date -u +%Y%m%dT%H%M%SZ)}"
cluster_id="${INSIGHT_CLUSTER_ID:-1}"
keyword="${INSIGHT_EVENT_KEYWORD:-validating-node-p4sa-audience}"

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
  printf '%s\t%s\n' "${duration_ms}" "${out_file}"
}

run_pair() {
  local scenario="$1"
  local operation="$2"
  local insight_label="$3"
  local kubectl_label="$4"
  local insight_func="$5"
  local kubectl_func="$6"

  local insight_result kubectl_result insight_ms kubectl_ms insight_out kubectl_out
  insight_result="$(run_timed "${insight_label}" "${insight_func}")"
  kubectl_result="$(run_timed "${kubectl_label}" "${kubectl_func}")"

  insight_ms="$(printf '%s' "${insight_result}" | awk '{print $1}')"
  insight_out="$(printf '%s' "${insight_result}" | awk '{print $2}')"
  kubectl_ms="$(printf '%s' "${kubectl_result}" | awk '{print $1}')"
  kubectl_out="$(printf '%s' "${kubectl_result}" | awk '{print $2}')"
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "${scenario}" "${operation}" "${insight_ms}" "${kubectl_ms}" "${insight_out}" "${kubectl_out}" \
    | tee -a "${out_dir}/summary.tsv"
}

insight_event_count() {
  "${kube_insight}" --db "${db_path}" query sql --output table --sql "
select
  reason.fact_value as reason,
  count(*) as retained_warning_events,
  min(datetime(reason.ts/1000,'unixepoch')) as first_seen,
  max(datetime(reason.ts/1000,'unixepoch')) as latest_seen
from object_facts reason
join object_facts typ
  on typ.version_id = reason.version_id
 and typ.fact_key = 'k8s_event.type'
where reason.cluster_id = ${cluster_id}
  and reason.fact_key = 'k8s_event.reason'
  and reason.fact_value = 'PolicyViolation'
  and typ.fact_value = 'Warning'
group by reason.fact_value;
"
}

kubectl_event_count() {
  "${kubectl_bin}" "${kubectl_context_args[@]}" \
    get events.events.k8s.io -A \
    --field-selector type=Warning,reason=PolicyViolation \
    --request-timeout=20s \
    -o name | wc -l
}

insight_event_edges() {
  "${kube_insight}" --db "${db_path}" query sql --output table --max-rows 20 --sql "
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
}

kubectl_event_payloads() {
  "${kubectl_bin}" "${kubectl_context_args[@]}" \
    get events.events.k8s.io -A \
    --field-selector type=Warning,reason=PolicyViolation \
    --request-timeout=20s \
    -o json
}

insight_event_message_search() {
  "${kube_insight}" --db "${db_path}" query sql --output table --max-rows 20 --sql "
select
  datetime(f.ts/1000,'unixepoch') as event_time,
  ev.namespace,
  ev.name,
  f.fact_value as message_preview
from object_facts f
join objects ev on ev.id = f.object_id
where f.cluster_id = ${cluster_id}
  and f.fact_key = 'k8s_event.message_preview'
  and lower(f.fact_value) like '%' || lower('${keyword}') || '%'
order by f.ts desc
limit 20;
"
}

kubectl_event_message_search() {
  "${kubectl_bin}" "${kubectl_context_args[@]}" \
    get events.events.k8s.io -A \
    --field-selector type=Warning \
    --request-timeout=20s \
    -o json | grep -ci "${keyword}" || true
}

insight_service_topology() {
  "${kube_insight}" --db "${db_path}" query sql --output table --max-rows 20 --sql "
select
  svc.namespace,
  svc.name as service,
  count(distinct es.src_id) as endpoint_slices,
  count(distinct pod.id) as pods
from object_edges es
join objects svc on svc.id = es.dst_id
left join object_edges ep
  on ep.src_id = es.src_id
 and ep.edge_type = 'endpointslice_targets_pod'
left join objects pod on pod.id = ep.dst_id
where es.cluster_id = ${cluster_id}
  and es.edge_type = 'endpointslice_for_service'
group by svc.namespace, svc.name
order by pods desc, endpoint_slices desc
limit 20;
"
}

kubectl_service_topology_inputs() {
  "${kubectl_bin}" "${kubectl_context_args[@]}" \
    get services,endpointslices.discovery.k8s.io,pods -A \
    --request-timeout=20s \
    -o name
}

insight_workload_inventory() {
  "${kube_insight}" --db "${db_path}" query sql --output table --max-rows 20 --sql "
select ok.kind, count(*) as latest_objects
from latest_index li
join object_kinds ok on ok.id = li.kind_id
where li.cluster_id = ${cluster_id}
  and ok.kind in (
    'Pod',
    'Service',
    'Deployment',
    'ReplicaSet',
    'StatefulSet',
    'DaemonSet',
    'Job',
    'CronJob',
    'EndpointSlice'
  )
group by ok.kind
order by latest_objects desc;
"
}

kubectl_workload_inventory() {
  "${kubectl_bin}" "${kubectl_context_args[@]}" \
    get pods,services,deployments.apps,replicasets.apps,statefulsets.apps,daemonsets.apps,jobs.batch,cronjobs.batch,endpointslices.discovery.k8s.io -A \
    --request-timeout=20s \
    -o name | wc -l
}

printf 'scenario\toperation\tinsight_ms\tkubectl_ms\tinsight_output\tkubectl_output\n' \
  | tee "${out_dir}/summary.tsv"

run_pair \
  "Retained PolicyViolation Event count" \
  "Count Warning PolicyViolation Events and show retained time window." \
  "insight-policy-count" \
  "kubectl-policy-count" \
  insight_event_count \
  kubectl_event_count

run_pair \
  "Event to affected resource investigation" \
  "Find Warning Events and join them to affected Kubernetes objects." \
  "insight-event-edges" \
  "kubectl-event-payloads" \
  insight_event_edges \
  kubectl_event_payloads

run_pair \
  "Event message keyword search" \
  "Search warning Event messages for a policy or failure keyword." \
  "insight-event-message-search" \
  "kubectl-event-message-search" \
  insight_event_message_search \
  kubectl_event_message_search

run_pair \
  "Service topology candidate list" \
  "List Services with EndpointSlice and Pod fan-out for topology triage." \
  "insight-service-topology" \
  "kubectl-service-topology-inputs" \
  insight_service_topology \
  kubectl_service_topology_inputs

run_pair \
  "Workload inventory for scope selection" \
  "Count common workload and routing objects across all namespaces." \
  "insight-workload-inventory" \
  "kubectl-workload-inventory" \
  insight_workload_inventory \
  kubectl_workload_inventory

echo "Benchmark output: ${out_dir}"
