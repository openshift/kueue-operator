#!/usr/bin/env bash
# Labels up to 4 worker nodes with a 2-level "datacenter" topology
# (block + rack) plus the node-group label used by the ResourceFlavor.
# Needs at least 2 worker nodes; 4 nodes shows off every block/rack
# combination.
set -euo pipefail

nodes=($(oc get nodes -l node-role.kubernetes.io/worker -o name))
if [ "${#nodes[@]}" -lt 2 ]; then
  echo "Need at least 2 worker nodes for the datacenter topology demo, found ${#nodes[@]}" >&2
  exit 1
fi

blocks=(b1 b1 b2 b2)
racks=(r1 r2 r1 r2)

max=${#nodes[@]}
if [ "$max" -gt 4 ]; then
  max=4
fi

for ((i = 0; i < max; i++)); do
  node="${nodes[$i]}"
  echo "Labeling ${node} with block=${blocks[$i]} rack=${racks[$i]}"
  oc label "${node}" \
    cloud.provider.com/topology-block="${blocks[$i]}" \
    cloud.provider.com/topology-rack="${racks[$i]}" \
    cloud.provider.com/node-group=tas-group \
    --overwrite
done
