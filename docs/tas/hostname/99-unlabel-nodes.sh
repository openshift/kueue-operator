#!/usr/bin/env bash
# Removes the instance-type label added by 00-label-nodes.sh.
set -euo pipefail

for node in $(oc get nodes -l node-role.kubernetes.io/worker -o name); do
  echo "Unlabeling ${node}"
  oc label "${node}" instance-type-
done
