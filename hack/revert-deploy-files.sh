#!/bin/bash
# Set back deploy scripts to original values

OPERATOR_IMAGE_REPLACE=$1
KUEUE_IMAGE_REPLACE=$2
hack/replace-image.sh deploy $OPERATOR_IMAGE_REPLACE OPERATOR_IMAGE 
hack/replace-image.sh deploy $KUEUE_IMAGE_REPLACE KUEUE_IMAGE
hack/replace-image.sh deploy/examples $KUEUE_IMAGE_REPLACE KUEUE_IMAGE
