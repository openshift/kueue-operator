#!/bin/bash

# $1 is the release (0.9, 0.10)
set -eou pipefail
REPO_ROOT=$HOME/Work/openshift/kueue-operator
KUEUE_MANIFEST_LIST=${KUEUE_MANIFEST_LIST-"https://github.com/kubernetes-sigs/kueue/releases/download/v$1/manifests.yaml"}
wget $KUEUE_MANIFEST_LIST -O kueue_manifest.yaml

rm -rf $REPO_ROOT/assets/kueue/$1
mkdir $REPO_ROOT/assets/kueue/$1
# Get all CRDs from release
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "CustomResourceDefinition")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/kueue.yaml
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "ClusterRole")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/clusterrole.yaml
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "ClusterRoleBinding")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/clusterrolebinding.yaml
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "Role")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/role.yaml
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "RoleBinding")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/rolebinding.yaml
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "APIService")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/apiservice.yaml
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "MutatingWebhookConfiguration")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/mutatingwebhook.yaml
podman run --rm -v "${PWD}":/workdir:z mikefarah/yq 'select(.kind == "ValidatingWebhookConfiguration")' /workdir/kueue_manifest.yaml > $REPO_ROOT/assets/kueue/$1/validatingwebhook.yaml


rm kueue_manifest.yaml
