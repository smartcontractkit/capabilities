#!/usr/bin/env bash
# Formats the protos written by hand with buf. Skips generated protos, which mark themselves the way generated Go does,
# and test fixtures under testdata, whose layout the tests may depend on.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

git ls-files --cached --others --exclude-standard -- '*.proto' ':(exclude,glob)**/testdata/**' |
  while IFS= read -r file; do
    if ! grep -qE '^// Code generated .* DO NOT EDIT\.$' "$file"; then
      buf format -w "$file"
    fi
  done
