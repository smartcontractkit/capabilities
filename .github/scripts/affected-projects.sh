#!/bin/bash

# Check if a branch input is provided
if [ -z "$1" ]; then
  echo "Please provide a branch."
  exit 1
fi

# Parse the input branch
base=$1
affected_projects=$(./nx show projects --affected --json --base=$base)
# echo "Affected projects:"
# echo "$affected_projects" | jq .

projects=($(echo $affected_projects | jq -r '.[]'))

# Initialize an empty string for output
output="{"

output+="\"base\": \"$base\","
output+="\"projects\": $affected_projects,"

# Loop through each project and collect nested details
for project in "${projects[@]}"; do
    project_info=$(./nx show project "$project" --json)
    
    # Get the root of the project
    project_root=$(echo $project_info | jq -r '.root')

    # Replace this with an actual command to get go.sum (if applicable) or any other desired info
    # This is an example; modify it to fit your needs
    project_go_sum=$(echo "$project_root/go.sum")

    # Append the result to the output string in a nested JSON format
    output+="\"$project\": { \"root\": \"$project_root\", \"go_sum\": \"$project_go_sum\" },"
done

# Remove the trailing comma and close the JSON object
output="${output%,}}"

# echo "Affected projects details:"
echo "$output" | jq .
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
# }
