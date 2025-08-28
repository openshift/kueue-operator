#!/usr/bin/env bash

set -eou pipefail

GO_BUILD_ENV="GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64}" make -C src build
