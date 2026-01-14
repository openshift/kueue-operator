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
  # This job is having difficult scheduling in OCP.
  "Should unsuspend a job and set nodeSelectors"
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

export GINKGO_ARGS="$GINKGO_ARGS --label-filter=feature:certs,feature:deployment,feature:e2e_v1beta1,feature:job,feature:jobset,feature:leaderworkerset,feature:statefulset,feature:visibility"

# shellcheck disable=SC2086
$GINKGO ${GINKGO_ARGS:-} \
  --skip="${GINKGO_SKIP_PATTERN}" \
  --junit-report=e2e-upstream-junit.xml \
  --json-report=e2e-upstream.json \
  --output-dir="$ARTIFACT_DIR" \
  --keep-going \
  --flake-attempts=3 \
  --no-color \
  -v ./upstream/kueue/src/test/e2e/"$E2E_TARGET_FOLDER"/...

