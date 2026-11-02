#!/bin/bash
set -euo pipefail

# List FBC images for a kueue version.
# Usage: get-fbc-images.sh <version>
#
# Queries Quay for FBC tags matching kueue-<version>__v<ocp>__kueue-rhel9-operator
# and inspects each for the bundle pullspec, commit, and NVR.

VERSION="${1:?Usage: $0 <version> (e.g. 1.4)}"

QUAY_REPO="quay.io/redhat-user-workloads/ocp-art-tenant/art-fbc"
TAG_PREFIX="kueue-v${VERSION}"
TAG_SUFFIX="kueue-rhel9-operator"

echo "==> Querying Quay for FBC tags matching: ${TAG_PREFIX}*${TAG_SUFFIX}"
echo ""

# Fetch tags from Quay API
TAGS=$(curl -sL "https://quay.io/api/v1/repository/redhat-user-workloads/ocp-art-tenant/art-fbc/tag/?filter_tag_name=like:${TAG_PREFIX}" \
  | jq -r --arg suffix "$TAG_SUFFIX" '.tags[]? | select(.name | endswith($suffix)) | .name' \
  | sort -V)

if [[ -z "$TAGS" ]]; then
  echo "No matching tags found."
  exit 1
fi

echo "==> Found FBC tags:"
echo ""

while read -r tag; do
  PULLSPEC="${QUAY_REPO}:${tag}"
  echo "--- Tag: $tag ---"
  echo "  Pullspec: $PULLSPEC"

  # Get digest
  DIGEST=$(skopeo inspect --raw "docker://${PULLSPEC}" 2>/dev/null | jq -r '.digest // empty' 2>/dev/null)
  if [[ -n "$DIGEST" ]]; then
    echo "  Digest: $DIGEST"
    echo "  Pullspec (by digest): ${QUAY_REPO}@${DIGEST}"
  fi

  # Inspect labels for build info
  LABELS=$(skopeo inspect "docker://${PULLSPEC}" 2>/dev/null | jq -r '{
    commit: (.Labels["vcs-ref"] // .Labels["io.openshift.build.commit.id"] // "unknown"),
    nvr: (.Labels["com.redhat.component"] // "unknown"),
    version: (.Labels["version"] // "unknown"),
    release: (.Labels["release"] // "unknown")
  }' 2>/dev/null) || LABELS='{"status": "could not inspect"}'
  echo "  Build info: $(echo "$LABELS" | jq -c '.')"
  echo ""
done <<< "$TAGS"

echo "==> For the release pipeline, use the pullspec-by-digest values above."
