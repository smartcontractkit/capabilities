# Remove directories that end with _generated
for dir in *_generated/; do
  if [ -d "$dir" ]; then
    echo "Removing directory: $dir"
    rm -rf "$dir"
  fi
done
