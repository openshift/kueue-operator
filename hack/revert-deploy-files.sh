#!/usr/bin/env bash
# Set back deploy scripts to original values

OPERATOR_IMAGE_REPLACE=$1
KUEUE_IMAGE_REPLACE=$2
MUST_GATHER_IMAGE_REPLACE=$3
hack/replace-image.sh deploy $OPERATOR_IMAGE_REPLACE OPERATOR_IMAGE
hack/replace-image.sh deploy $KUEUE_IMAGE_REPLACE KUEUE_IMAGE
hack/replace-image.sh deploy/examples $KUEUE_IMAGE_REPLACE KUEUE_IMAGE
hack/replace-image.sh deploy $MUST_GATHER_IMAGE_REPLACE MUST_GATHER_IMAGE
# Fix the RELATED_IMAGE_OPERAND_IMAGE env var value.
find deploy/ -type f -exec sed -i '/- name: RELATED_IMAGE_OPERAND_IMAGE/{n;s/value: OPERATOR_IMAGE/value: KUEUE_IMAGE/}' {} \;
