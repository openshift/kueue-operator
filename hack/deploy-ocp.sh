#!/bin/bash

OPERATOR_IMAGE_REPLACE=$1
KUEUE_IMAGE_REPLACE=$2
hack/replace-image.sh deploy OPERATOR_IMAGE $OPERATOR_IMAGE_REPLACE
hack/replace-image.sh deploy KUEUE_IMAGE $KUEUE_IMAGE_REPLACE

# Deploy the operator
oc apply -f deploy/
oc apply -f deploy/crd/

# Set back deploy scripts to original values
hack/replace-image.sh deploy $OPERATOR_IMAGE_REPLACE OPERATOR_IMAGE 
hack/replace-image.sh deploy $KUEUE_IMAGE_REPLACE KUEUE_IMAGE

echo "oc apply -f deploy/examples/job.yaml"
echo "will deploy default openshift installation"

