#!/bin/bash

# Check if a base input is provided
if [ -z "$1" ]; then
  echo "Please provide a branch."
  exit 1
fi

base=$1
exclude_integration_test=false

# Check for the exclude-integration-test flag
if [ "$2" == "exclude-integration-test" ]; then
  exclude_integration_test=true
fi

# Determine the affected projects command based on the flag
if [ "$exclude_integration_test" = true ]; then
  affected_projects=$(./nx show projects --affected --json --base=$base | jq 'del(.[] | select(. == "integration_tests"))')
else
  affected_projects=$(./nx show projects --affected --json --base=$base)
fi

# echo "Affected projects:"
# echo "$affected_projects" | jq .

projects=($(echo $affected_projects | jq -r '.[]'))

# Initialize an output string
output="{ \"base\": \"$base\", \"projects\": $affected_projects, "

# Loop through each project and collect nested details
for project in "${projects[@]}"; do
    project_info=$(./nx show project "$project" --json)
    project_root=$(echo $project_info | jq -r '.root')
    project_go_sum=$(echo "$project_root/go.sum")

    # Append the result to the output string in a nested JSON format
    output+="\"$project\": { \"root\": \"$project_root\", \"go_sum\": \"$project_go_sum\" },"
done

if [ ${#projects[@]} -eq 0 ]; then
  output+=" \"run_checks\": false"
else
  output+=" \"run_checks\": true"
fi

# Remove the trailing comma and close the JSON object
output+=" }"

# echo "Affected projects details:"
echo $output
# Outputs:
# {
#   "base": "main",
#   "projects": [
#     "project_name_1",
#     "project_name_2",
#   ],
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
