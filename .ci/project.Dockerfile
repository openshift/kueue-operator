FROM registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.23-openshift-4.19

RUN curl -sSL -o /usr/bin/operator-sdk https://github.com/operator-framework/operator-sdk/releases/download/v1.37.0/operator-sdk_linux_amd64 && \
    chmod +x /usr/bin/operator-sdk

WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .

RUN echo "Generating OPERATOR_IMAGE and KUEUE_IMAGE..." && \
    REVISION=$(git log --oneline -1 | awk '{print $4}' | tr -d "'") && \
    export OPERATOR_IMAGE="quay.io/rh-pbhojara/testrepo/kueue-operator:latest" && \
    make get-kueue-image && \
    export KUEUE_IMAGE=$(< .kueue_image) && \
    hack/update-deploy-files.sh ${OPERATOR_IMAGE} ${KUEUE_IMAGE} && \
    operator-sdk generate bundle --input-dir deploy/ --version 0.1.0
