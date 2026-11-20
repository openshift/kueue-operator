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

# Temporary, test-only workaround until CI has a supported Red Hat Go 1.27 builder.
# This is not ideal: it can download an upstream toolchain and bypasses the builder's
# compliance wrapper, which forces GOTOOLCHAIN=local even when explicitly overridden.
# Remove this workaround once the test runner's builder provides Go 1.27.
bootstrap_go=$(command -v go.real || command -v go)
toolchain_root=$(
  GOEXPERIMENT='' GOTOOLCHAIN=go1.27.4 "$bootstrap_go" env GOROOT
)
export GOROOT="$toolchain_root"
export PATH="$GOROOT/bin:$PATH"
export GOEXPERIMENT=
export GOTOOLCHAIN=local

echo "Using upstream Go toolchain for E2E tests (temporary workaround):"
go version

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
      # TAS suites live below singlecluster/tasapi and share generic feature labels such as
      # feature:job with this lane, so exclude their area label explicitly.
      echo "(feature:certs || feature:cohort || feature:deployment || feature:disaggregatedset || feature:e2e_v1beta1 || feature:job || feature:jobset || feature:leaderworkerset || feature:metrics || feature:statefulset || feature:visibility) && !area:tas"
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
  # The "Metrics access control" test enables the MetricsAccessControl feature gate and
  # directly patches the kueue deployment, but the kueue-operator reconciles the deployment
  # back — so the test cannot pass in an operator-managed cluster.
  "Metrics access control"
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

# Prints the frameworks of the cluster Kueue CR, one per line.
function kueue_frameworks() {
  $OC get kueue.kueue.openshift.io/cluster -o go-template='{{range .spec.config.integrations.frameworks}}{{.}}{{"\n"}}{{end}}'
}

DISAGGREGATEDSET_ADDED=false

# Removes DisaggregatedSet from the Kueue CR when the runner added it and the
# script exits before the normal restore, keeping the original exit status.
# shellcheck disable=SC2317 # invoked through the EXIT trap
function restore_disaggregatedset_on_exit() {
  local status=$?
  if [ "$DISAGGREGATEDSET_ADDED" = true ]; then
    restore_kueue_for_folder singlecluster || echo "Error: failed to remove DisaggregatedSet from the cluster Kueue CR" >&2
  fi
  exit "$status"
}

function configure_kueue_for_folder() {
  local folder="$1"
  case "$folder" in
    singlecluster)
      # The upstream DisaggregatedSet cases need the DisaggregatedSet integration,
      # which requires a LeaderWorkerSet that serves disaggregatedset.x-k8s.io/v1.
      local frameworks_output
      if ! frameworks_output=$(kueue_frameworks); then
        echo "Error: failed to read the cluster Kueue CR frameworks" >&2
        return 1
      fi
      if ! grep -qx "DisaggregatedSet" <<< "$frameworks_output"; then
        echo "Adding DisaggregatedSet to the cluster Kueue CR frameworks for singlecluster..."
        $OC patch kueue.kueue.openshift.io/cluster --type=json -p \
          '[{"op":"add","path":"/spec/config/integrations/frameworks/-","value":"DisaggregatedSet"}]'
        DISAGGREGATEDSET_ADDED=true
        trap restore_disaggregatedset_on_exit EXIT
        # Let's wait for the reconciliation to complete. The test suite checks if kueue-controller-manager is available.
        $OC wait kueue.kueue.openshift.io/cluster --for=condition=Available="False" --timeout=120s
      fi
      ;;
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
    dra/capacity)
      echo "Replacing deviceClassMappings with capacity sources for consumable capacity devices..."
      $OC patch kueue.kueue.openshift.io/cluster --type=merge -p \
        '{"spec":{"config":{"resources":{"deviceClassMappings":[{"name":"gpu.memory","deviceClassNames":["gpu.example.com"],"sources":[{"type":"Capacity","capacity":{"name":"memory","driver":"gpu.example.com","deviceSelector":{"type":"CEL","cel":{"expression":"device.driver == '"'"'gpu.example.com'"'"'"}}}}]}]}}}}'
      $OC wait kueue.kueue.openshift.io/cluster --for=condition=Available="False" --timeout=120s
      ;;
  esac
}

function restore_kueue_for_folder() {
  local folder="$1"
  case "$folder" in
    singlecluster)
      if [ "$DISAGGREGATEDSET_ADDED" = true ]; then
        local frameworks_output index
        local -a frameworks
        if ! frameworks_output=$(kueue_frameworks); then
          echo "Error: failed to read the cluster Kueue CR frameworks; DisaggregatedSet may still be enabled" >&2
          return 1
        fi
        mapfile -t frameworks <<< "$frameworks_output"
        for index in "${!frameworks[@]}"; do
          if [ "${frameworks[$index]}" = "DisaggregatedSet" ]; then
            echo "Removing DisaggregatedSet from the cluster Kueue CR frameworks..."
            if ! $OC patch kueue.kueue.openshift.io/cluster --type=json -p \
              "[{\"op\":\"test\",\"path\":\"/spec/config/integrations/frameworks/${index}\",\"value\":\"DisaggregatedSet\"},{\"op\":\"remove\",\"path\":\"/spec/config/integrations/frameworks/${index}\"}]"; then
              echo "Error: failed to remove DisaggregatedSet from the cluster Kueue CR" >&2
              return 1
            fi
            break
          fi
        done
        # DisaggregatedSet was removed, or is confirmed absent.
        DISAGGREGATEDSET_ADDED=false
      fi
      ;;
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
    dra/capacity)
      echo "Removing capacity sources from Kueue CR..."
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
  folder_ginkgo_args=()
  if [ -n "${GINKGO_ARGS:-}" ]; then
    read -r -a folder_ginkgo_args <<< "$GINKGO_ARGS"
  fi
  if [ -n "$label_filter" ]; then
    folder_ginkgo_args+=("--label-filter=$label_filter")
  fi

  report_name="${folder//\//-}"
  "$GINKGO" "${folder_ginkgo_args[@]}" \
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

