#!/bin/bash
set -e

# This script:
# 1. Finds all modules.
# 2. Maps changed files (passed as a param) to found modules.
# 3. Filters out modules that contain ignored paths (if provided).
# 4. Prints out the affected modules.
# 5. Output the result (as JSON) to a GitHub Actions environment variable.

# Get the list of changed files as parameter (from JSON array)
list_of_changed_files=$1
if [[ -z "$list_of_changed_files" ]]; then
  echo "No changed files provided as a 1st parameter. Exiting."
  exit 1
fi

# Validate JSON before parsing
if ! echo "$list_of_changed_files" | jq . >/dev/null 2>&1; then
  echo "Error: Invalid JSON provided for changed files: $list_of_changed_files"
  echo "Defaulting to empty array"
  list_of_changed_files='[]'
fi

changed_files=$(echo "$list_of_changed_files" | jq -r '.[]')
echo "Changed files: $changed_files"

# Get the list of ignored paths as parameter (from JSON array), if provided
list_of_ignored_paths=$2
if [[ -n "$list_of_ignored_paths" ]]; then
  ignored_paths=$(echo "$list_of_ignored_paths" | jq -r '.[]')
  echo "Ignored paths: $ignored_paths"
fi

# 1. Find all modules in the repository,
# - Strip the leading './' from the path
# (it is necessary for comparison, 
# the affected files do not have leading './')
modules=$(find . -name 'go.mod' -exec dirname {} \; | sed 's|^./||' | uniq)
echo "Found all modules in the repo: $modules"

# Use an associative array to track unique modules
declare -A unique_modules

# 2. Iterate over changed files path and map them to the corresponding module
for path_to_file in $changed_files; do
  echo "Resolving a module affected by a file: '$path_to_file'"
  # the flag that indicates if the path matches any module
  is_path_in_modules=false
  for module in $modules; do
    echo "Validating against module: '$module'"
    # if a module's name matches with a file path
    # add it to the affected modules array, skipping the root (`.`)
    if [[ $module != "." && $path_to_file =~ ^$module* ]]; then
      echo -e "File '$path_to_file' mapped to the module '$module'\n"
      unique_modules["$module"]="$module"
      is_path_in_modules=true
      break
    fi
  done

  # if no module matched the list of affected files, default to a root (`.`)
  if [[ $is_path_in_modules == false ]]; then
    echo "File '$path_to_file' did not match any module, defaulting to root '.'"
    unique_modules["."]="."
    is_path_in_modules=false
  fi
  is_path_in_modules=false
done

# if the path is empty (for any reason), it will not get to the loop,
# so if the unique_modules array is empty, default to the root module
if [[ ${#unique_modules[@]} -eq 0 ]]; then
  echo "No files were changed, defaulting to the root module '.'"
  unique_modules["."]="."
fi

# 3. Filter out modules that contain ignored paths 
# (only if affected_modules array is not empty)
if [[ ${#unique_modules[@]} -gt 0 && -n "$ignored_paths" ]]; then
  echo "Filtering out modules containing ignored paths..."
  declare -A filtered_modules

  for module in "${!unique_modules[@]}"; do
    should_ignore=false
    for ignored_path in $ignored_paths; do
      # Check if the module path contains the ignored path
      if [[ $module == *"$ignored_path"* ]]; then
        echo "Ignoring module '$module' because it contains ignored path '$ignored_path'"
        should_ignore=true
        break
      fi
    done

    if [[ $should_ignore == false ]]; then
      filtered_modules["$module"]="$module"
    fi
  done

  # Replace the original array with the filtered one
  unique_modules=()
  for key in "${!filtered_modules[@]}"; do
    unique_modules["$key"]="$key"
  done

  # If all modules were filtered out, default to the root module
  if [[ ${#unique_modules[@]} -eq 0 ]]; then
    echo "All modules were filtered out, defaulting to the root module '.'"
    unique_modules["."]="."
  fi
fi

# Convert keys (module names) of the associative array to an indexed array
affected_modules=("${!unique_modules[@]}")
echo "Affected modules: ${affected_modules[@]}"

# Convert bash array to a JSON array for GitHub Actions
json_array=$(printf '%s\n' "${affected_modules[@]}" | jq -R . | jq -s . | jq -c)
echo "module_names=$json_array" >> "$GITHUB_OUTPUT"
