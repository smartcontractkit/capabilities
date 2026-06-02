#!/usr/bin/env bash
#
# Validate that every intra-repo module pin resolves to a commit reachable
# from the default branch.
#
# This repo is a multi-module monorepo: modules require sibling modules
# (e.g. chain_capabilities/evm -> libs, -> chain_capabilities/common) by
# pseudo-version (vX.Y.Z-<timestamp>-<12-hex-sha>). There are no `replace`
# directives wiring these locally, so the pinned SHA must be reachable from
# the default branch, or external consumers (and fresh builds here) break
# with "unknown revision".
#
# This guards against pinning to a non-default-branch commit (a PR/feature
# commit), which is ephemeral: once the branch is gone, the SHA is GC'd and
# every consumer fails. See `make update-common-capabilities`, which should
# only ever be pointed at a commit already on the default branch.
#
# Usage: validate-intra-module-refs.sh [base-ref]
#   base-ref defaults to origin/main.
set -euo pipefail

BASE_REF="${1:-origin/main}"
MODULE_PREFIX="github.com/smartcontractkit/capabilities"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

if ! git rev-parse --verify --quiet "$BASE_REF" >/dev/null; then
  echo "error: base ref '$BASE_REF' not found. Fetch it first (git fetch origin main)." >&2
  exit 2
fi

fail=0
seen_file="$(mktemp)"
trap 'rm -f "$seen_file"' EXIT

# All go.mod files except vendored ones.
while IFS= read -r gomod; do
  # Lines that pin an intra-repo module to a pseudo-version. Matches both
  # require lines and `=> module version` replace targets.
  while IFS= read -r line; do
    # module is the token starting with the repo prefix; version is the next token.
    module="$(printf '%s\n' "$line" | grep -oE "${MODULE_PREFIX}[a-zA-Z0-9._/-]*" | head -1)"
    version="$(printf '%s\n' "$line" | grep -oE "v[0-9][^[:space:]]*" | head -1)"
    [ -z "$module" ] && continue
    [ -z "$version" ] && continue

    # Only pseudo-versions carry a -<14-digit-timestamp>-<12-hex> suffix.
    sha="$(printf '%s\n' "$version" | grep -oE '\-[0-9]{14}-[0-9a-f]{12}$' | grep -oE '[0-9a-f]{12}$' || true)"
    [ -z "$sha" ] && continue                 # tagged release, not a pseudo-version
    [ "$sha" = "000000000000" ] && continue   # zero pseudo-version (replaced module)

    key="${module}@${sha}"
    grep -qxF "$key" "$seen_file" && continue
    printf '%s\n' "$key" >> "$seen_file"

    # Reachable from the default branch? Treat "not an ancestor" and
    # "object missing/GC'd" alike as failures.
    if git merge-base --is-ancestor "$sha" "$BASE_REF" 2>/dev/null; then
      echo "ok   ${key}  (reachable from ${BASE_REF}, ${gomod#./})"
    else
      echo "FAIL ${key}  (NOT reachable from ${BASE_REF}; pinned in ${gomod#./})"
      fail=1
    fi
  done < <(grep -E "${MODULE_PREFIX}[a-zA-Z0-9._/-]* v[0-9]" "$gomod" || true)
done < <(find . -name go.mod -not -path '*/vendor/*' -type f | sort)

if [ "$fail" -ne 0 ]; then
  cat >&2 <<EOF

One or more intra-repo module pins reference a commit that is not on
${BASE_REF}. This usually means a sibling module was pinned to a
PR/feature-branch commit instead of a merged one. Merge the dependency to
the default branch first, then re-pin to a commit that lives there
(see: make update-common-capabilities REF=<commit-on-default-branch>).
EOF
  exit 1
fi

echo "All intra-repo module pins are reachable from ${BASE_REF}."
