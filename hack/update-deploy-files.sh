#!/usr/bin/env bash
# set replace image placeholders with real values

OPERATOR_IMAGE_REPLACE=$1
KUEUE_IMAGE_REPLACE=$2
hack/replace-image.sh deploy OPERATOR_IMAGE $OPERATOR_IMAGE_REPLACE
hack/replace-image.sh deploy KUEUE_IMAGE $KUEUE_IMAGE_REPLACE
