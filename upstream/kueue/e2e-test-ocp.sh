#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2164
# e2e-test-ocp.sh

set -o errexit
set -o nounset
set -o pipefail

OC=$(which oc) # OpenShift CLI
export OC
GINKGO=$(pwd)/bin/ginkgo
export GINKGO
SOURCE_DIR="$(cd "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
export KUEUE_NAMESPACE="openshift-kueue-operator"
# This is required to reuse the exisiting code.
# Set this to empty value for OCP tests.
export E2E_KIND_VERSION=""

# Space-separated list of e2e test folders to run (e.g. "singlecluster certmanager").
E2E_TARGET_FOLDERS="${E2E_TARGET_FOLDERS:-${E2E_TARGET_FOLDER:-singlecluster}}"

function label_worker_nodes() {
  echo "Labeling two worker nodes for e2e tests..."
  # Retrieve the names of nodes with the "worker" role.
  local nodes
  IFS=' ' read -r -a nodes <<< "$($OC get nodes -l node-role.kubernetes.io/worker -o jsonpath='{.items[*].metadata.name}')"

  if [ ${#nodes[@]} -lt 2 ]; then
    echo "Error: Found less than 2 worker nodes. Cannot assign labels."
    exit 1
  fi

  # Label the first node as "on-demand"
  $OC patch node "${nodes[0]}" --type=merge -p '{"metadata":{"labels":{"instance-type":"on-demand"}}}'
  # Label the second node as "spot"
  $OC patch node "${nodes[1]}" --type=merge -p '{"metadata":{"labels":{"instance-type":"spot"}}}'
  echo "Labeled ${nodes[0]} as on-demand and ${nodes[1]} as spot."
}

function allow_privileged_access {
  $OC adm policy add-scc-to-group privileged system:authenticated system:serviceaccounts
  $OC adm policy add-scc-to-group anyuid system:authenticated system:serviceaccounts
}

# Returns the ginkgo --label-filter value for a given target folder.
function ginkgo_label_filter() {
  local folder="$1"
  case "$folder" in
    singlecluster)
      echo "feature:certs,feature:deployment,feature:e2e_v1beta1,feature:job,feature:jobset,feature:leaderworkerset,feature:metrics,feature:statefulset,feature:visibility"
      ;;
    certmanager)
      echo "!feature:prometheus"
      ;;
    dra)
      echo ""
      ;;
    sequential)
      echo "feature:admissionfairsharing"
      ;;
    *)
      echo ""
      ;;
  esac
}

skips=(
  "StatefulSet created with WorkloadPriorityClass"
  "LeaderWorkerSet created with WorkloadPriorityClass"
)

skipsRegex=$(
  IFS="|"
  printf "%s" "${skips[*]}"
)

GINKGO_SKIP_PATTERN="($skipsRegex)"

pushd "${SOURCE_DIR}" >/dev/null
. utils.sh
apply_patches
popd >/dev/null

# Label two worker nodes for e2e tests (similar to the Kind setup).
label_worker_nodes

# Disable scc rules for e2e pod tests
allow_privileged_access

function configure_kueue_for_folder() {
  local folder="$1"
  case "$folder" in
    sequential)
      echo "Patching cluster Kueue CR for sequential (admissionFairSharing)..."
      $OC patch kueue.kueue.openshift.io/cluster --type=merge -p \
        '{"spec":{"config":{"admissionFairSharing":{"configuration":"Custom","custom":{"usageHalfLifeTimeSeconds":1,"usageSamplingIntervalSeconds":1}}}}}'
      # Let's wait for the reconciliation to complete. The test suite checks if kueue-controller-manager is available.
      $OC wait kueue.kueue.openshift.io/cluster --for=condition=Available="False" --timeout=120s
      ;;
    dra/counter)
      echo "Replacing deviceClassMappings with counter sources for partitionable devices..."
      $OC patch kueue.kueue.openshift.io/cluster --type=merge -p \
        '{"spec":{"config":{"resources":{"deviceClassMappings":[{"name":"gpu.memory","deviceClassNames":["gpu.example.com"],"sources":[{"type":"Counter","counter":{"name":"memory","driver":"gpu.example.com","deviceSelector":{"type":"CEL","cel":{"expression":"device.driver == '"'"'gpu.example.com'"'"'"}}}}]}]}}}}'
      $OC wait kueue.kueue.openshift.io/cluster --for=condition=Available="False" --timeout=120s
      ;;
  esac
}

function restore_kueue_for_folder() {
  local folder="$1"
  case "$folder" in
    sequential)
      echo "Removing admissionFairSharing from cluster Kueue CR..."
      $OC patch kueue.kueue.openshift.io/cluster --type=json -p \
        '[{"op":"remove","path":"/spec/config/admissionFairSharing"}]'
      ;;
    dra/counter)
      echo "Removing counter sources from Kueue CR..."
      $OC patch kueue.kueue.openshift.io/cluster --type=merge -p \
        '{"spec":{"config":{"resources":{"deviceClassMappings":[{"name":"gpu","deviceClassNames":["gpu.example.com"]},{"name":"gpu-late-dc","deviceClassNames":["gpu-late-dc.example.com"]}]}}}}'
      ;;
  esac
}

exit_code=0
for folder in $E2E_TARGET_FOLDERS; do
  echo "=== Running upstream e2e tests for: ${folder} ==="
  configure_kueue_for_folder "$folder"
  label_filter=$(ginkgo_label_filter "$folder")
  folder_ginkgo_args="${GINKGO_ARGS:-}"
  if [ -n "$label_filter" ]; then
    folder_ginkgo_args="$folder_ginkgo_args --label-filter=$label_filter"
  fi

  report_name="${folder//\//-}"
  # shellcheck disable=SC2086
  $GINKGO ${folder_ginkgo_args} \
    --skip="${GINKGO_SKIP_PATTERN}" \
    --junit-report="e2e-upstream-${report_name}-junit.xml" \
    --json-report="e2e-upstream-${report_name}.json" \
    --output-dir="$ARTIFACT_DIR" \
    --keep-going \
    --flake-attempts=3 \
    --no-color \
    -v "./upstream/kueue/src/test/e2e/${folder}/..." || exit_code=$?

  restore_kueue_for_folder "$folder"
done
exit $exit_code

