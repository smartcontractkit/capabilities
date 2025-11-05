#!/bin/bash
set -euo pipefail

GO_GENERATOR_TOOL="$1"
GO_EXECUTABLE="$2"
# Extract directory from the go executable path and convert to absolute path
# Bazel provides relative paths, so we need to resolve them
if [[ "${GO_EXECUTABLE}" = /* ]]; then
    # Already absolute
    GO_EXECUTABLE_PATH=$(dirname "${GO_EXECUTABLE}")
else
    # Relative path - resolve to absolute using realpath if available, otherwise construct from PWD
    if command -v realpath >/dev/null 2>&1; then
        GO_EXECUTABLE_PATH=$(dirname "$(realpath "${GO_EXECUTABLE}")")
    else
        # Fallback: construct absolute path from current directory
        GO_EXECUTABLE_PATH=$(cd "$(dirname "${GO_EXECUTABLE}")" && pwd)
    fi
fi
echo "GO_EXECUTABLE_PATH: ${GO_EXECUTABLE_PATH}"
export PATH="${GO_EXECUTABLE_PATH}:${PATH}"
echo "PATH: ${PATH}"

find "." -name "*-schema.json" | while read -r SCHEMA_FILE_PATH; do
    echo "Processing schema file: ${SCHEMA_FILE_PATH}"
    SCHEMA_FILE_DIR=$(dirname "${SCHEMA_FILE_PATH}" | sed 's|^./||')
 
    "$GO_GENERATOR_TOOL" --dir "${SCHEMA_FILE_DIR}" --extra_urls https://raw.githubusercontent.com/smartcontractkit/chainlink-common/refs/heads/main/pkg/capabilities/consensus/ocr3/ocr3cap/ocr3cap_common-schema.json


    # Directory from the schema file; removing the leading ./
    COPY_DIR="${SCHEMA_FILE_DIR}"
    OUTPUT_DIR="${BUILD_WORKSPACE_DIRECTORY}/${SCHEMA_FILE_DIR}"

    # Copy only files ending with _generated.go from the schema directory to the workspace root
    find "${COPY_DIR}" -name "*_generated.go" -type f | while read -r GENERATED_FILE_PATH; do
        # Get the relative path from COPY_DIR to preserve directory structure
        RELATIVE_PATH="${GENERATED_FILE_PATH#${COPY_DIR}/}"
        DEST_FILE_PATH="${OUTPUT_DIR}/${RELATIVE_PATH}"

        # Create destination directory if it doesn't exist
        mkdir -p "$(dirname "${DEST_FILE_PATH}")"
        cp "${GENERATED_FILE_PATH}" "${DEST_FILE_PATH}"
    done
done
