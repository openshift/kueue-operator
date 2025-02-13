#!/bin/bash

oc delete -f deploy/examples/job.yaml
oc delete -f deploy/crd/
oc delete -f deploy/
# this is to a fix bug
oc delete mutatingwebhookconfigurations.admissionregistration.k8s.io kueue-mutating-webhook-configuration
oc delete validatingwebhookconfigurations.admissionregistration.k8s.io kueue-validating-webhook-configuration
oc get crds | grep kueue | awk '{print $1}' | xargs oc delete crd
oc delete -f hack/manifests/cert-manager-rh.yaml
oc delete namespaces cert-manager
