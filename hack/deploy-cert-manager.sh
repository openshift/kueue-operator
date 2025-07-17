#!/bin/bash
set -xeou pipefail

OCP_MINOR=$(oc get clusterversion version -o jsonpath='{.status.desired.version}' | cut -d. -f2)
echo "Detected OCP minor version: ${OCP_MINOR}"

TARGET=$((OCP_MINOR - 3))
echo "Target cert-manager minor version: ${TARGET}"

# Wait for packagemanifests to be populated
echo "Waiting for packagemanifests to be available..."
timeout 300s bash -c '
  while true; do
    if oc get packagemanifest openshift-cert-manager-operator -n openshift-marketplace >/dev/null 2>&1; then
      # Additional check to ensure the packagemanifest has channels populated
      CHANNELS=$(oc get packagemanifest openshift-cert-manager-operator -n openshift-marketplace -o jsonpath="{.status.channels[*].name}" 2>/dev/null || echo "")
      if [[ -n "$CHANNELS" ]]; then
        echo "PackageManifest found with channels: $CHANNELS"
        break
      else
        echo "PackageManifest found but no channels yet, waiting..."
      fi
    else
      echo "PackageManifest not found, waiting..."
    fi
    sleep 5
  done
'

# Get package manifest JSON with retry mechanism to handle race condition.
echo "Retrieving package manifest JSON..."
PACKAGE_MANIFEST=""
for i in {1..10}; do
  if PACKAGE_MANIFEST=$(oc get packagemanifest openshift-cert-manager-operator -n openshift-marketplace -o json 2>/dev/null); then
    echo "Successfully retrieved package manifest on attempt $i"
    break
  else
    echo "Failed to retrieve package manifest on attempt $i, retrying in 3s..."
    sleep 3
  fi
done

if [[ -z "$PACKAGE_MANIFEST" ]]; then
  echo "ERROR: Failed to retrieve package manifest after 10 attempts"
  oc get packagemanifests -n openshift-marketplace --no-headers 2>/dev/null | grep -i cert-manager || echo "No cert-manager related packages found"
  exit 1
fi

# Find version-specific channels matching pattern stable-vX.Y
COMPATIBLE_CHANNELS=$(echo "$PACKAGE_MANIFEST" | jq -r --arg target "$TARGET" '
  .status.channels[] | 
  select(.name | test("stable-v[0-9]+\\.[0-9]+$")) |
  .name as $name |
  ($name | split("-v")[1] | split(".") | map(tonumber)) as $version |
  {
    name: $name,
    major: $version[0],
    minor: $version[1],
    currentCSV: .currentCSV
  } |
  select(.minor <= ($target | tonumber))
')

SELECTED_CHANNEL=$(echo "$COMPATIBLE_CHANNELS" | jq -s 'sort_by(.minor) | last')

# Fallback to default channel if no version-specific match
if [[ -z "$SELECTED_CHANNEL" || "$SELECTED_CHANNEL" == "null" ]]; then
  echo "No version-specific channel found, using default channel"
  DEFAULT_CHANNEL=$(echo "$PACKAGE_MANIFEST" | jq -r '.status.defaultChannel')
  SELECTED_CHANNEL=$(echo "$PACKAGE_MANIFEST" | jq -r --arg ch "$DEFAULT_CHANNEL" '
    .status.channels[] | select(.name == $ch) | {
      name: .name,
      major: (.name | split("-v")[1] | split(".")[0] | tonumber),
      minor: (.name | split("-v")[1] | split(".")[1] | tonumber),
      currentCSV: .currentCSV
    }
  ')
fi

CHANNEL_NAME=$(echo "$SELECTED_CHANNEL" | jq -r '.name')
STARTING_CSV=$(echo "$SELECTED_CHANNEL" | jq -r '.currentCSV')
MAJOR_VER=$(echo "$SELECTED_CHANNEL" | jq -r '.major')
MINOR_VER=$(echo "$SELECTED_CHANNEL" | jq -r '.minor')

echo "Selected channel: ${CHANNEL_NAME}"
echo "Starting CSV: ${STARTING_CSV}"

oc apply -f - <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: cert-manager-operator
  labels:
    name: cert-manager-operator
EOF

# Create appropriate OperatorGroup based on version
if [[ $MAJOR_VER -ge 1 ]] && [[ $MINOR_VER -ge 15 ]]; then
  echo "Creating AllNamespaces OperatorGroup for v1.15+"
  oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: openshift-cert-manager-operator
  namespace: cert-manager-operator
spec: {}
EOF
else
  echo "Creating SingleNamespace OperatorGroup for pre-v1.15"
  oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: openshift-cert-manager-operator
  namespace: cert-manager-operator
spec:
  targetNamespaces:
  - "cert-manager-operator"
EOF
fi

oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: openshift-cert-manager-operator
  namespace: cert-manager-operator
spec:
  channel: ${CHANNEL_NAME}
  name: openshift-cert-manager-operator
  source: redhat-operators
  sourceNamespace: openshift-marketplace
  installPlanApproval: Automatic
  startingCSV: ${STARTING_CSV}
EOF
