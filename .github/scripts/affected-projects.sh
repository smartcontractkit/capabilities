#!/bin/bash

# Check if a base input is provided
if [ -z "$1" ]; then
  echo "Please provide a branch."
  exit 1
fi

base=$1
echo "DEBUG: Base branch received: $base" # Debugging: Show the base branch

affected_projects=$(./nx show projects --affected --json --base=$base)
echo "DEBUG: Raw Nx affected projects output: $affected_projects" # Debugging: Show raw Nx output

# Check if affected_projects is empty or invalid JSON before processing
if [ -z "$affected_projects" ] || ! echo "$affected_projects" | jq . > /dev/null 2>&1; then
  echo "ERROR: 'nx show projects --affected --json --base=$base' returned empty or invalid JSON."
  # Provide a default empty JSON or exit gracefully if this is an error condition for your workflow
  echo "{ \"base\": \"$base\", \"projects\": [], \"run_checks\": false }"
  exit 0
fi

projects=($(echo "$affected_projects" | jq -r '.[]')) # Use quotes for variable expansion
echo "DEBUG: Parsed affected projects array: ${projects[@]}" # Debugging: Show parsed projects

# Initialize an output string
output="{ \"base\": \"$base\", \"projects\": $affected_projects, "

targets=("test" "race" "build")

for target in "${targets[@]}"; do
  projects_with_target=$(./nx show projects --affected -t "$target" --json --base="$base") # Use quotes for variables
  echo "DEBUG: Projects with target '$target' raw Nx output: $projects_with_target" # Debugging: Show raw Nx output for target

  if [ "$target" == "test" ]; then
    # Ensure projects_with_target is valid JSON before piping to jq
    if echo "$projects_with_target" | jq . > /dev/null 2>&1; then
      projects_with_target=$(echo "$projects_with_target" | jq 'del(.[] | select(. == "integration_tests"))')
      echo "DEBUG: Projects with '$target' after filtering 'integration_tests': $projects_with_target" # Debugging: Show after filtering
    else
      echo "WARNING: Skipping 'integration_tests' filter for '$target' due to invalid JSON: $projects_with_target"
      projects_with_target="[]" # Default to empty array if invalid
    fi
  fi

  output+="\"projects_with_$target\": $projects_with_target, "

  if [ $(echo "$projects_with_target" | jq 'length') -eq 0 ]; then # Use jq to get array length
    output+=" \"run_$target\": false, "
    echo "DEBUG: run_$target set to false" # Debugging: Show run_target status
  else
    output+=" \"run_$target\": true, "
    echo "DEBUG: run_$target set to true" # Debugging: Show run_target status
  fi
done

# Loop through each project and collect nested details
for project in "${projects[@]}"; do
    project_info=$(./nx show project "$project" --json)
    echo "DEBUG: Project info for '$project': $project_info" # Debugging: Show raw project info

    # Check if project_info is valid JSON before processing
    if echo "$project_info" | jq . > /dev/null 2>&1; then
      project_root=$(echo "$project_info" | jq -r '.root')
      project_go_sum=$(echo "$project_root/go.sum")
      echo "DEBUG: Root for '$project': $project_root, go_sum: $project_go_sum" # Debugging: Show root and go_sum

      # Append the result to the output string in a nested JSON format
      output+="\"$project\": { \"root\": \"$project_root\", \"go_sum\": \"$project_go_sum\" },"
    else
      echo "ERROR: 'nx show project $project --json' returned invalid JSON: $project_info"
      output+="\"$project\": { \"root\": \"\", \"go_sum\": \"\" }," # Default to empty values if invalid
    fi
done

if [ ${#projects[@]} -eq 0 ]; then
  output+=" \"run_checks\": false"
  echo "DEBUG: run_checks set to false (no affected projects)"
else
  output+=" \"run_checks\": true"
  echo "DEBUG: run_checks set to true (affected projects found)"
fi

# Remove the trailing comma and close the JSON object
# This assumes the last added item before closing does not end with a comma, or it handles it.
# A more robust way would be to build an array of key-value pairs and then join them.
output+=" }"

# Use jq to pretty-print and validate the final JSON output before echoing it
final_json_output=$(echo "$output" | jq '.')
echo "DEBUG: Final JSON output to be passed to GITHUB_OUTPUT:" # Debugging: Show final JSON
echo "$final_json_output" # Pretty print for readability in logs

# Check if the final output is valid JSON
if ! echo "$final_json_output" | jq . > /dev/null 2>&1; then
  echo "CRITICAL ERROR: The final JSON output is invalid!"
  echo "$final_json_output" # Output the malformed JSON for inspection
  exit 1 # Fail the script if the final output is not valid JSON
fi

echo "$final_json_output" # This is the actual output that will be captured by the GitHub Action