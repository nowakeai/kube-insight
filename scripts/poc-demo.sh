#!/usr/bin/env bash
set -euo pipefail

out_dir="${1:-testdata/generated/poc-demo}"
db_path="${2:-${out_dir}/kube-insight-poc-demo.db}"
validation_json="${out_dir}/validation.json"
service_json="${out_dir}/service-investigation.json"

rm -rf "${out_dir}"
rm -f "${db_path}" "${db_path}-wal" "${db_path}-shm"
mkdir -p "${out_dir}"

go run ./cmd/kube-insight validate poc \
  --fixtures testdata/fixtures/kube \
  --output "${out_dir}/samples" \
  --db "${db_path}" \
  --clusters 1 \
  --copies 2 \
  --query-runs 3 \
  > "${validation_json}"

awk -F'"' '/"summaryText":/ { print "Validation: " $4 }' "${validation_json}"

go run ./cmd/kube-insight investigate service api-0001 \
  --cluster cluster-gen-01 \
  --namespace ns-01-0001 \
  --db "${db_path}" \
  --from 1970-01-01 \
  --to 2100-01-01 \
  --max-evidence-objects 5 \
  --max-versions-per-object 3 \
  > "${service_json}"

echo "Validation report: ${validation_json}"
echo "Service evidence bundle: ${service_json}"
