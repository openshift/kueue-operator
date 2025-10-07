#!/usr/bin/env bash

set -eou pipefail

. utils.sh

apply_patches

GO_BUILD_ENV="GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}" make -C src build
