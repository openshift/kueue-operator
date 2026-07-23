#!/usr/bin/env bash
# Labels every worker node with instance-type=on-demand so the hostname
# ResourceFlavor (03-resourceflavor.yaml) can match them and TAS can discover
# one topology domain per node.
set -euo pipefail

for node in $(oc get nodes -l node-role.kubernetes.io/worker -o name); do
  echo "Labeling ${node}"
  oc label "${node}" instance-type=on-demand --overwrite
done
