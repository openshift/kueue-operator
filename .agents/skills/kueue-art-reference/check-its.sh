#!/bin/bash
set -euo pipefail

# Check Konflux integration test results for FBC Snapshots.
# Usage: check-its.sh <pullspec1>,<pullspec2>,...
#
# Finds Snapshots in the tenant namespace that contain the given pullspecs,
# then prints the integration test scenario results.

PULLSPECS="${1:?Usage: $0 <pullspec1>,<pullspec2>,...}"
NAMESPACE="${NAMESPACE:-art-kueue-tenant}"

echo "==> Looking for Snapshots containing the given pullspecs in ${NAMESPACE}..."

IFS=',' read -ra SPECS <<< "$PULLSPECS"

# Find snapshots that contain any of the given pullspecs
SNAPSHOTS=()
while IFS= read -r snap; do
  [[ -z "$snap" ]] && continue
  spec_json=$(oc get snapshot "$snap" -n "$NAMESPACE" -o json 2>/dev/null) || continue
  for spec in "${SPECS[@]}"; do
    spec=$(echo "$spec" | xargs)  # trim whitespace
    if echo "$spec_json" | jq -e --arg s "$spec" '.spec.components[]? | select(.containerImage == $s)' &>/dev/null; then
      SNAPSHOTS+=("$snap")
      break
    fi
  done
done < <(oc get snapshots -n "$NAMESPACE" --sort-by=.metadata.creationTimestamp -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null | tail -50)

if [[ ${#SNAPSHOTS[@]} -eq 0 ]]; then
  echo "No matching Snapshots found."
  exit 1
fi

echo "==> Found ${#SNAPSHOTS[@]} matching Snapshot(s)."
echo ""

for snap in "${SNAPSHOTS[@]}"; do
  echo "--- Snapshot: $snap ---"
  # Get integration test status from annotations
  oc get snapshot "$snap" -n "$NAMESPACE" -o json 2>/dev/null \
    | jq -r '
      .metadata.annotations // {} |
      to_entries[] |
      select(.key | startswith("test.appstudio.openshift.io/status")) |
      {
        scenario: (.key | split(".") | last),
        result: (.value | fromjson? // .value)
      }
    ' 2>/dev/null | yq -P '.' 2>/dev/null || echo "  (no test results found)"
  echo ""
done
