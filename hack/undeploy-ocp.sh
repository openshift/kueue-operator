#!/bin/bash

oc delete workloads.kueue.x-k8s.io --all -A
oc delete localqueues.kueue.x-k8s.io --all -A
oc delete clusterqueues.kueue.x-k8s.io --all -A
oc delete resourceflavors.kueue.x-k8s.io --all -A
oc delete -f test/e2e/bindata/assets/08_kueue_default.yaml
oc delete -f deploy/crd/
oc delete -f deploy/
oc get crds | grep kueue | awk '{print $1}' | xargs oc delete crd
oc delete -f hack/manifests/cert-manager-rh.yaml
oc delete namespaces cert-manager
