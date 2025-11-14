apply_patches() {
  for patch in patch/*.patch; do
    pushd src >/dev/null
    # Check if patch can be applied (not already applied)
    if git apply --check ../$patch 2>/dev/null; then
      echo "Applying $patch"
      git apply ../$patch || {
        echo "Error: Failed to apply $patch"
        popd >/dev/null
        return 1
      }
    else
      echo "Skipping $patch (already applied or conflicts)"
    fi
    popd >/dev/null
  done
}

revert_patches() {
  for patch in patch/*.patch; do
    pushd src >/dev/null
    # Check if patch can be reverted (is currently applied)
    if git apply --reverse --check ../$patch 2>/dev/null; then
      echo "Reverting $patch"
      git apply -R ../$patch || {
        echo "Error: Failed to revert $patch"
        popd >/dev/null
        return 1
      }
    else
      echo "Skipping $patch (not applied or conflicts)"
    fi
    popd >/dev/null
  done
}
