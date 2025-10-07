apply_patches() {
  for patch in patch/*.patch; do
    pushd src >/dev/null
    echo "Applying $patch"
    git apply ../$patch 2>/dev/null
    popd >/dev/null
  done
}
