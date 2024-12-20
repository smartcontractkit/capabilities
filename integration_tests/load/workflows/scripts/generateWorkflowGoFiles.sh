#!/bin/bash

# Check if the number of directories to create is provided
if [ -z "$1" ]; then
  echo "Usage: $0 <number_of_directories>"
  exit 1
fi

num_dirs=$1
mkdir -p workflowwasmfiles_generated 
template_dir="templateworkflowwasmfile"
base_dir="workflowwasmfiles_generated"

# Create the base directory if it doesn't exist
mkdir -p "$base_dir"

for i in $(seq 1 $num_dirs); do
  new_dir="$base_dir/workflow$i"
  mkdir -p "$new_dir"

  # Copy the main.go file from the template directory
  cp "$template_dir/main.go" "$new_dir/main.go"

  # Update the ReportID in the copied main.go file
  sed -i '' "s/ReportID: \"0001\"/ReportID: \"$(printf '%04d' $i)\"/" "$new_dir/main.go"

  echo "Created $new_dir with updated ReportID"
done
