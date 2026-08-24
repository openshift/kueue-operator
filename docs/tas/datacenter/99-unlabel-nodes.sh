#!/usr/bin/env bash
# Removes the labels added by 00-label-nodes.sh.
set -euo pipefail

nodes=($(oc get nodes -l cloud.provider.com/node-group=tas-group -o name))
for node in "${nodes[@]}"; do
  echo "Unlabeling ${node}"
  oc label "${node}" \
    cloud.provider.com/topology-block- \
    cloud.provider.com/topology-rack- \
    cloud.provider.com/node-group-
done
