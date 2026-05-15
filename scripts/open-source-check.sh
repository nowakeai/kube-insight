#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "${root}"

required_files=(
  README.md
  CONTRIBUTING.md
  SECURITY.md
  SUPPORT.md
  MAINTAINERS.md
  CODE_OF_CONDUCT.md
  RELEASE.md
  Dockerfile
  .goreleaser.yaml
  .github/dependabot.yml
  .github/pull_request_template.md
  .github/ISSUE_TEMPLATE/bug_report.yml
  .github/ISSUE_TEMPLATE/config.yml
  .github/ISSUE_TEMPLATE/feature_request.yml
  .github/workflows/ci.yml
  .github/workflows/release.yml
  assets/brand/kube-insight-logo.svg
)

missing=0
for file in "${required_files[@]}"; do
  if [ ! -f "${file}" ]; then
    echo "missing required file: ${file}" >&2
    missing=1
  fi
done
if [ "${missing}" -ne 0 ]; then
  exit 1
fi

if git rev-list --objects --all | grep -E '(^|/)todos\.md$|kubeinsight\.db|\.db(-shm|-wal)?$|\.sqlite3?$'; then
  echo "reachable git history contains a local todo or database artifact" >&2
  exit 1
fi

if git grep -n -I -E 'gke_gcp-cluster|gcp-cluster-1|gke-general' -- . ':!scripts/open-source-check.sh'; then
  echo "current tree contains environment-specific cluster names" >&2
  exit 1
fi

credential_pattern='AKIA[0-9A-Z]{16}|-----BEGIN (RSA |OPENSSH |EC |DSA )?PRIVATE KEY-----|ghp_[0-9A-Za-z]{36,}|github_pat_[0-9A-Za-z_]{20,}|xox[baprs]-[0-9A-Za-z-]{20,}|AIza[0-9A-Za-z_-]{35}'
if git grep -n -I -E "${credential_pattern}" -- .; then
  echo "current tree contains a high-risk credential pattern" >&2
  exit 1
fi

for rev in $(git rev-list --all); do
  if git grep -n -I -E "${credential_pattern}|gke_gcp-cluster|gcp-cluster-1|gke-general" "${rev}" -- . ':!scripts/open-source-check.sh'; then
    echo "reachable git history contains a sensitive pattern at ${rev}" >&2
    exit 1
  fi
done

echo "open-source readiness checks passed"
