#!/bin/bash
# Installs the protofetch version in .tool-versions, unless it is already installed.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
version=$(awk '$1 == "protofetch" { print $2 }' "$root/.tool-versions")
if [[ -z "$version" ]]; then
    echo "protofetch is missing from $root/.tool-versions"
    exit 1
fi

if command -v protofetch >/dev/null && [[ "$(protofetch --version)" == "protofetch $version" ]]; then
    echo "protofetch up-to-date @ $version"
    exit 0
fi

cargo install protofetch --version "$version" --locked
