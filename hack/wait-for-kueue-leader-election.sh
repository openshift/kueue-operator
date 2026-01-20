#!/bin/bash
set -eou pipefail

# wait-for-kueue-lease.sh waits for the kueue-controller-manager to elect a leader
# by checking the controller logs for leader election messages. This is useful for 
# ensuring the kueue controller manager is running and has elected a leader.

TIMEOUT_SECONDS=300
RETRY_INTERVAL=5

echo "Waiting for kueue-controller-manager leader election..."
echo "Timeout: ${TIMEOUT_SECONDS} seconds, Retry interval: ${RETRY_INTERVAL} seconds"

START_TIME=$(date +%s)
END_TIME=$((START_TIME + TIMEOUT_SECONDS))

while true; do
  CURRENT_TIME=$(date +%s)
  
  if [ $CURRENT_TIME -ge $END_TIME ]; then
    echo "ERROR: Timeout reached. Leader election not detected after ${TIMEOUT_SECONDS} seconds"
    exit 1
  fi
  
  # Check if the deployment exists first
  if ! oc get deployment kueue-controller-manager -n openshift-kueue-operator >/dev/null 2>&1; then
    ELAPSED=$((CURRENT_TIME - START_TIME))
    echo "kueue-controller-manager deployment not found after ${ELAPSED} seconds, retrying in ${RETRY_INTERVAL} seconds..."
    sleep ${RETRY_INTERVAL}
    continue
  fi
  
  # Check logs for leader election messages
  if oc logs deployment/kueue-controller-manager -n openshift-kueue-operator --tail=-1 --all-containers=true 2>/dev/null | grep "successfully acquired lease"; then
    ELAPSED=$((CURRENT_TIME - START_TIME))
    echo "SUCCESS: Leader election detected in kueue-controller-manager logs after ${ELAPSED} seconds"
    exit 0
  else
    ELAPSED=$((CURRENT_TIME - START_TIME))
    echo "Leader election not detected after ${ELAPSED} seconds, retrying in ${RETRY_INTERVAL} seconds..."
    sleep ${RETRY_INTERVAL}
  fi
done 
