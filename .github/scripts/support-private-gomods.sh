#!/usr/bin/env bash

set -euo pipefail

##
# Supports private Go modules to be fetched from its own repo in GitHub.
# Expects env vars GITHUB_TOKEN and GOPRIVATE to be set.
##

if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "::error::GITHUB_TOKEN is not set. Exiting."
  exit 1
fi

if [[ -z "${GOPRIVATE:-}" ]]; then
  echo "::error::GOPRIVATE is not set. Exiting."
  exit 1
fi

echo "::debug::GOPRIVATE: $GOPRIVATE"


git config --global \
    url."https://x-access-token:${GITHUB_TOKEN}@github.com/".insteadOf \
    "https://github.com/"

go env -w GOPRIVATE="${GOPRIVATE}"
