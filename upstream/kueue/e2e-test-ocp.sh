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

# To label worker nodes for e2e tests.
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

skips=(
  # do not deploy AppWrapper in OCP
  AppWrapper
  # do not deploy PyTorch in OCP
  PyTorch
  # do not deploy JobSet in OCP
  TrainJob
  # do not deploy LWS in OCP
  JAX
  # do not deploy KubeRay in OCP
  Kuberay
  # metrics setup is different than our OCP setup
  Metrics
  # ring -> we do not enable Fair sharing by default in our operator
  Fair
  # we do not enable this feature in our operator
  TopologyAwareScheduling
  # For tests that rely on CPU setup, we need to fix upstream to get cpu allocatables from node
  # rather than hardcoding CPU limits.
  # relies on particular CPU setup to force pods to not schedule
  "Failed Pod can be replaced in group"
  "should allow to schedule a group of diverse pods"
  "StatefulSet created with WorkloadPriorityClass"
  "LeaderWorkerSet created with WorkloadPriorityClass"
  "Pod groups when Single CQ"
  # We do not have kueuectl in our operator
  "Kueuectl"
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

# shellcheck disable=SC2086
$GINKGO ${GINKGO_ARGS:-} \
  --skip="${GINKGO_SKIP_PATTERN}" \
  --junit-report=junit.xml \
  --json-report=e2e.json \
  --output-dir="$ARTIFACTS" \
  --keep-going \
  --flake-attempts=3 \
  -v ./upstream/kueue/src/test/e2e/"$E2E_TARGET_FOLDER"/...

