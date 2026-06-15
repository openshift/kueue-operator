#!/usr/bin/env bash
# shellcheck disable=SC2164

apply_patches() {
  for patch in patch/*.patch; do
    pushd src >/dev/null
    # Check if patch is already applied (reverse-apply succeeds)
    if git apply --check --reverse ../"$patch" 2>/dev/null; then
      echo "Skipping $patch (already applied)"
    else
      echo "Applying $patch"
      git apply ../"$patch" || {
        echo "Error: Failed to apply $patch — patch may need updating after a submodule sync"
        popd >/dev/null
        return 1
      }
    fi
    popd >/dev/null
  done
}
