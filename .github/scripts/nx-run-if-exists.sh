#!/bin/bash

# Check if both a project name and target are provided
if [[ -z "$1" || -z "$2" ]]; then
  echo "Usage: $0 <project-name> <target>"
  exit 1
fi

# Assign the first argument to the project variable and the second to the target variable
project=$1
target=$2

# Run the nx show project command and capture the output
output=$(./nx show project "$project" --json)

# Check if the target exists using jq
if echo "$output" | jq -e ".targets.${target}" > /dev/null; then
  # If the target exists, run the task and capture the exit code
  echo "Task '$target' exists in project '$project'. Running it..."
  ./nx run "$project:$target"
else
  # If the target doesn't exist, print message and exit with 0
  echo "Task '$target' does not exist in project '$project'. Skipping..."
  exit 0
fi
