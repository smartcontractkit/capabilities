#!/bin/bash

# to ensure affected projects are correctly detected provide base branch and head sha
base=$1
head=$2

# Check if a `base` input is provided
if [ -z "$base" ]; then
  echo "Please provide a base branch (to compare against) as the first argument."
  exit 1
# Check if a `head` input is provided
elif [ -z "$head" ]; then
  echo "Please provide a head sha as the second argument."
  exit 1
fi

# to ensure affected projects are correctly detected use `--base` and `--head`
affected_projects=$(./nx show projects --affected --json --base=$base --head=$head)

# Uncomment echo lines for debugging
# echo "Affected projects:"
# echo "$affected_projects" | jq .

projects=($(echo $affected_projects | jq -r '.[]'))

# Initialize an output string
output="{ \"base\": \"$base\", \"head\": \"$head\", \"projects\": $affected_projects, "

targets=("test" "race" "build")

for target in "${targets[@]}"; do
  # to ensure affected projects are correctly detected use `--base` and `--head`
  projects_with_target=$(./nx show projects --affected -t $target --json --base=$base --head=$head)

  if [ "$target" == "test" ]; then
    projects_with_target=$(echo $projects_with_target | jq 'del(.[] | select(. == "integration_tests"))')
  fi

  output+="\"projects_with_$target\": $projects_with_target, "

  if [ ${#projects_with_target[@]} -eq 0 ]; then
    output+=" \"run_$target\": false, "
  else
    output+=" \"run_$target\": true, "
  fi
done

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
#   "head": "f6756431b2cc499526be6b3edccedb1678191921",
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
