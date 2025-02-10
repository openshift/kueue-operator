#!/bin/bash

oc delete -f deploy/examples/job.yaml
oc delete -f deploy/crd/
oc delete -f deploy/
# this is to a fix bug
oc delete mutatingwebhookconfigurations.admissionregistration.k8s.io kueue-mutating-webhook-configuration
oc delete validatingwebhookconfigurations.admissionregistration.k8s.io kueue-validating-webhook-configuration
