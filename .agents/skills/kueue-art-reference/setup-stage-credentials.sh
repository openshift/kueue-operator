#!/bin/bash
set -euo pipefail

STAGE_AUTH="${1:?Usage: $0 <stage-registry-auth-token>}"

echo "==> Logging into stage registry with podman..."
STAGE_USER=$(echo "$STAGE_AUTH" | base64 -d | cut -d: -f1)
echo "$STAGE_AUTH" | base64 -d | cut -d: -f2- \
  | podman login -u "$STAGE_USER" --password-stdin registry.stage.redhat.io

echo "==> Adding stage registry credentials to the cluster pull secret..."
if oc whoami &>/dev/null && \
   oc get secret/pull-secret -n openshift-config -o jsonpath='{.data.\.dockerconfigjson}' \
     | base64 -d \
     | jq --arg auth "$STAGE_AUTH" '.auths["registry.stage.redhat.io"] = {"auth": $auth}' \
     | oc set data secret/pull-secret -n openshift-config --from-file=.dockerconfigjson=/dev/stdin; then
  echo "    Cluster pull secret updated."
else
  echo "    Skipped (no cluster or insufficient permissions)."
fi

echo "==> Done. Stage registry credentials configured."
