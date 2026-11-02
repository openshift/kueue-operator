#!/bin/bash
set -euo pipefail

# Inspect FBC image content: related images, bundles, source commits, NVRs.
# Usage: get-fbc-content.sh <fbc-pullspec>
#
# This script works with any FBC pullspec. It:
# 1. Discovers the related-images.json OCI artifact
# 2. Pulls the rendered catalog.json
# 3. Inspects each related image for source repo, commit, and NVR

FBC_PULLSPEC="${1:?Usage: $0 <fbc-pullspec>}"

echo "==> Inspecting FBC image: $FBC_PULLSPEC"
echo ""

# Try to get the manifest and find related-images artifact
MANIFEST=$(skopeo inspect --raw "docker://${FBC_PULLSPEC}" 2>/dev/null) || {
  echo "ERROR: Could not inspect $FBC_PULLSPEC"
  exit 1
}

# Check if it's a manifest list (multi-arch)
MEDIA_TYPE=$(echo "$MANIFEST" | jq -r '.mediaType // .schemaVersion' 2>/dev/null)
if [[ "$MEDIA_TYPE" == *"manifest.list"* ]] || [[ "$MEDIA_TYPE" == *"image.index"* ]]; then
  echo "Multi-arch image detected. Selecting amd64 manifest..."
  AMD64_DIGEST=$(echo "$MANIFEST" | jq -r '.manifests[] | select(.platform.architecture == "amd64") | .digest' 2>/dev/null)
  if [[ -n "$AMD64_DIGEST" ]]; then
    # Re-inspect the amd64-specific manifest
    REPO=$(echo "$FBC_PULLSPEC" | cut -d@ -f1 | cut -d: -f1)
    FBC_PULLSPEC="${REPO}@${AMD64_DIGEST}"
    MANIFEST=$(skopeo inspect --raw "docker://${FBC_PULLSPEC}" 2>/dev/null)
  fi
fi

# Extract catalog.json from the image
echo "==> Extracting catalog content..."
TMPDIR=$(mktemp -d)
trap "rm -rf $TMPDIR" EXIT

# Try oras first, fall back to skopeo+tar
if command -v oras &>/dev/null; then
  oras pull "$FBC_PULLSPEC" -o "$TMPDIR" 2>/dev/null || true
fi

CATALOG_FILE=""
if [[ -f "$TMPDIR/catalog.json" ]]; then
  CATALOG_FILE="$TMPDIR/catalog.json"
elif [[ -f "$TMPDIR/configs/catalog.json" ]]; then
  CATALOG_FILE="$TMPDIR/configs/catalog.json"
else
  # Fall back to skopeo copy + extract
  skopeo copy "docker://${FBC_PULLSPEC}" "dir:${TMPDIR}/image" 2>/dev/null || true
  # Try to find catalog.json in extracted layers
  for layer in "$TMPDIR"/image/*.tar.gz "$TMPDIR"/image/*.tar; do
    [[ -f "$layer" ]] || continue
    tar -xf "$layer" -C "$TMPDIR" 2>/dev/null || true
  done
  for f in "$TMPDIR"/catalog.json "$TMPDIR"/configs/catalog.json; do
    [[ -f "$f" ]] && CATALOG_FILE="$f" && break
  done
fi

if [[ -z "$CATALOG_FILE" ]]; then
  echo "WARNING: Could not extract catalog.json. Falling back to image labels."
  skopeo inspect "docker://${FBC_PULLSPEC}" 2>/dev/null | jq '{labels: .Labels}' || true
  exit 0
fi

echo "==> Catalog content:"
echo ""

# Parse and display related images
jq -r '
  select(.schema == "olm.bundle") |
  "Bundle: \(.name)\n  Package: \(.package)\n  Image: \(.image)\n  Related images:" +
  ([.relatedImages[]? | "    - \(.name): \(.image)"] | join("\n"))
' "$CATALOG_FILE" 2>/dev/null || cat "$CATALOG_FILE"

echo ""
echo "==> Inspecting related images for source info..."
echo ""

# Get unique images and inspect each
jq -r '[.relatedImages[]?.image // empty] | unique[]' "$CATALOG_FILE" 2>/dev/null | head -20 | while read -r img; do
  echo "--- Image: $img ---"
  LABELS=$(skopeo inspect "docker://${img}" 2>/dev/null | jq -r '{
    source: .Labels["vcs-url"] // .Labels["io.openshift.build.source-location"] // "unknown",
    commit: .Labels["vcs-ref"] // .Labels["io.openshift.build.commit.id"] // "unknown",
    nvr: .Labels["com.redhat.component"] // "unknown",
    version: .Labels["version"] // "unknown",
    release: .Labels["release"] // "unknown"
  }' 2>/dev/null) || LABELS='{"error": "could not inspect"}'
  echo "$LABELS" | jq '.'
  echo ""
done
