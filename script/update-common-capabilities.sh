#!/usr/bin/env bash
set -euo pipefail

REF="${1:?Usage: $0 <branch-or-commit>}"
MODULE="github.com/smartcontractkit/capabilities/chain_capabilities/common"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

for chain in aptos evm solana; do
  dir="${REPO_ROOT}/chain_capabilities/${chain}"
  if [ ! -f "${dir}/go.mod" ]; then
    echo "SKIP: ${dir}/go.mod not found"
    continue
  fi
  echo "Updating ${chain}..."
  (cd "$dir" && go get "${MODULE}@${REF}" && go mod tidy)
  echo "Done: ${chain}"
done

echo "All done."
