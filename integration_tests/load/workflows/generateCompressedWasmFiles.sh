#!/bin/bash

# Check if the directory is provided
if [ -z "$1" ]; then
  echo "Usage: $0 <directory>"
  exit 1
fi

base_dir=$1

# Check if the provided directory exists
if [ ! -d "$base_dir" ]; then
  echo "Directory $base_dir does not exist."
  exit 1
fi

# Iterate over each subdirectory in the base directory
for sub_dir in "$base_dir"/*/; do
  if [ -d "$sub_dir" ]; then
    sub_dir_name=$(basename "$sub_dir")
    wasm_file="$sub_dir/$sub_dir_name.wasm"
    compressed_file="$sub_dir/$sub_dir_name.brotli"

    if [ -f "$wasm_file" ]; then
      echo "Compressing: $wasm_file to $compressed_file"
      go run brotli_compress.go "$wasm_file" "$compressed_file"
    else
      echo "Failed to build $wasm_file"
    fi
  fi
done