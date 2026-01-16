#!/bin/bash

# Check if a base input is provided
if [ -z "$1" ]; then
  echo "Please provide a base branch as the 1st argument." >&2
  exit 1
fi

base=$1

# --base and --head are necessary for comparison
# tail -1 is needed to avoid extra log lines from nx
affected_projects=$(./nx show projects --affected --json --base=$base --head=HEAD | tail -1)

echo "Affected projects:" >&2
if echo "$affected_projects" | jq . >/dev/null 2>&1; then
  echo "Valid JSON output:" >&2
  echo "$affected_projects" | jq . >&2
else
  echo "Raw output (not valid JSON):" >&2
  echo "$affected_projects" >&2
  exit 1
fi

echo "Parsing projects..." >&2
projects=($(echo $affected_projects | jq -r '.[]'))
echo "Projects parsed." >&2

# Use jq to build JSON properly
json_output=$(jq -n --arg base "$base" --argjson projects "$affected_projects" '{
  "base": $base,
  "projects": $projects
}')

echo "Adding target projects to JSON..." >&2
targets=("test" "race" "build")
for target in "${targets[@]}"; do
  # Add --head=HEAD to ensure consistency (compared against --base)
  projects_with_target=$(./nx show projects --affected -t $target --json --base=$base --head=HEAD)

  if [ "$target" == "test" ]; then
    projects_with_target=$(echo $projects_with_target | jq 'del(.[] | select(. == "integration_tests"))')
  fi

  json_output=$(echo "$json_output" | jq --argjson target_projects "$projects_with_target" --arg target "$target" \
    '. + {"projects_with_\($target)": $target_projects}')

  # Check array length correctly using jq
  projects_count=$(echo "$projects_with_target" | jq 'length')
  if [ "$projects_count" -eq 0 ]; then
    json_output=$(echo "$json_output" | jq --arg target "$target" \
      '. + {"run_\($target)": false}')
  else
    json_output=$(echo "$json_output" | jq --arg target "$target" \
      '. + {"run_\($target)": true}')
  fi
  echo "Target projects for '$target' added to JSON." >&2
done

# Loop through each project and collect nested details
echo "Collecting project details..." >&2
for project in "${projects[@]}"; do
  if [ -n "$project" ]; then
    project_info=$(./nx show project "$project" --json)
    project_root=$(echo $project_info | jq -r '.root')
    
    # Check if go.sum exists in project root
    if [ -f "$project_root/go.sum" ]; then
      project_go_sum="$project_root/go.sum"
    else
      # Look one level deeper for go.sum when not found on root level
      found_go_sum=""
      for subdir in "$project_root"/*/; do
        if [ -f "$subdir/go.sum" ]; then
          found_go_sum="${subdir}go.sum"
          project_root="$subdir"
          break
        fi
      done
      
      if [ -n "$found_go_sum" ]; then
        project_go_sum="$found_go_sum"
      else
        project_go_sum="$project_root/go.sum"  # fallback to original path
      fi
    fi

    # Add project details to JSON using jq
    json_output=$(echo "$json_output" | jq --arg project "$project" --arg root "$project_root" --arg go_sum "$project_go_sum" \
      '. + {($project): {"root": $root, "go_sum": $go_sum}}')
  fi
  echo "Details for project '$project' added to JSON." >&2
done

# Add run_checks flag
echo "Adding 'run_checks' flags..." >&2
if [ ${#projects[@]} -eq 0 ]; then
  json_output=$(echo "$json_output" | jq '. + {"run_checks": false}')
else
  json_output=$(echo "$json_output" | jq '. + {"run_checks": true}')
fi
echo "Run checks flag added." >&2

# Output the final JSON (this goes to stdout for GH Actions)
echo "$json_output" | jq -c .
# Outputs:
# {
#   "base": "main",
#   "projects": [
#     "project_name_1",
#     "project_name_2",
#   ],
#   "projects_with_target_1": [
#     "project_name_1"
#   ],
#   "run_target_1": true,
#   "projects_with_target_2": [
#     "project_name_1",
#     "project_name_2"
#   ],
#   "run_target_2": true,
#   "projects_with_target_3": [],
#   "run_target_3": false,
#   "project_name_1": {
#     "root": "project_1_root",
#     "go_sum": "project_1_root/go.sum"
#   },
#   "project_name_2": {
#     "root": "project_2_root",
#     "go_sum": "project_2_root/go.sum"
#   }
#   "run_checks": true # or false if no projects are affected
# }