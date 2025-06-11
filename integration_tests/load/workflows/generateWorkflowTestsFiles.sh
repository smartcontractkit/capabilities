#!/bin/bash

# Check if the number of workflows to create is provided
if [ -z "$1" ]; then
  echo "Usage: $0 <number_of_workflows>"
  exit 1
fi



# Number of directories to create
num_dirs=$1

# Generate workflow Go files
./scripts/generateWorkflowGoFiles.sh "$num_dirs"

# Compile Go files to WebAssembly
./scripts/generateWasmFiles.sh "workflowwasmfiles_generated"

./generateCompressedWasmFiles.sh "workflowwasmfiles_generated"

