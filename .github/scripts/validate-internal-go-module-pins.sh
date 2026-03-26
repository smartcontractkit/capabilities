#!/usr/bin/env bash

set -euo pipefail

go_mod_file="${1:-go.mod}"
base_ref="${GITHUB_BASE_REF:-main}"
remote_base_ref="origin/${base_ref}"

if [[ ! -f "${go_mod_file}" ]]; then
  echo "go.mod file not found: ${go_mod_file}" >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required to validate internal Go module pins" >&2
  exit 1
fi

if ! git rev-parse --verify --quiet "${remote_base_ref}^{commit}" >/dev/null; then
  git fetch origin "${base_ref}:refs/remotes/origin/${base_ref}"
fi

module_json="$(go mod edit -modfile="${go_mod_file}" -json)"
current_module="$(jq -r '.Module.Path' <<<"${module_json}")"

failures=0

while IFS=$'\t' read -r dep_path dep_version; do
  [[ -z "${dep_path}" || -z "${dep_version}" ]] && continue
  [[ "${dep_path}" == "${current_module}" ]] && continue
  [[ "${dep_path}" == github.com/smartcontractkit/capabilities* ]] || continue

  if [[ "${dep_version}" =~ ^v.+-([0-9]{14})-([0-9a-f]{12})$ ]]; then
    dep_sha="${BASH_REMATCH[2]}"
  else
    continue
  fi

  if ! git rev-parse --verify --quiet "${dep_sha}^{commit}" >/dev/null; then
    echo "Internal module ${dep_path}@${dep_version} points to ${dep_sha}, which is not present in this checkout." >&2
    failures=1
    continue
  fi

  if ! git merge-base --is-ancestor "${dep_sha}" "${remote_base_ref}"; then
    echo "Internal module ${dep_path}@${dep_version} points to ${dep_sha}, which is not reachable from ${remote_base_ref}." >&2
    echo "This is unsafe with squash merges. Repin it to a commit already on ${base_ref} or merge that dependency first." >&2
    failures=1
  fi
done < <(jq -r '.Require[]? | [.Path, .Version] | @tsv' <<<"${module_json}")

exit "${failures}"
