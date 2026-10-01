#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<EOF
Usage: make update-chain-capabilities MODULE=<module> REF=<branch-or-commit>
   or: $0 <module> <branch-or-commit>

  MODULE  Go module path to update in each chain capability
  REF     Branch name, tag, or commit SHA to pin the module to

Examples:
  make update-chain-capabilities MODULE=github.com/smartcontractkit/chainlink-common REF=main
  make update-chain-capabilities MODULE=github.com/smartcontractkit/capabilities/libs REF=my-feature-branch
  make update-chain-capabilities MODULE=github.com/smartcontractkit/chainlink-common REF=1d3a14a9b049
EOF
  exit 1
}

MODULE="${1:-}"
REF="${2:-}"

if [ -z "$MODULE" ]; then
  echo "ERROR: missing MODULE" >&2
  usage
fi
if [ -z "$REF" ]; then
  echo "ERROR: missing REF" >&2
  usage
fi

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

for chain in aptos evm solana stellar; do
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
