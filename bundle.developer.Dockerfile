FROM registry.redhat.io/ubi9/ubi@sha256:8f1496d50a66e41433031bf5bdedd4635520e692ccd76ffcb649cf9d30d669af as builder
RUN dnf -y install jq

ARG RELATED_IMAGE_FILE=related_images.developer.json
ARG CSV_FILE=bundle/manifests/kueue-operator.clusterserviceversion.yaml
ARG OPERATOR_IMAGE_ORIGINAL=registry.redhat.io/kueue/kueue-rhel9-operator:latest
ARG OPERAND_IMAGE_ORIGINAL=registry.redhat.io/kueue/kueue-rhel9:latest
ARG MUSTGATHER_IMAGE_ORIGINAL=registry.redhat.io/kueue/kueue-must-gather-rhel9:latest

COPY ${CSV_FILE} /manifests/kueue-operator.clusterserviceversion.yaml
COPY ${RELATED_IMAGE_FILE} /${RELATED_IMAGE_FILE}

RUN OPERATOR_IMAGE=$(jq -r '.[] | select(.name == "operator") | .image' /${RELATED_IMAGE_FILE}) && sed -i "s|${OPERATOR_IMAGE_ORIGINAL}.*|${OPERATOR_IMAGE}|g" /manifests/kueue-operator.clusterserviceversion.yaml
RUN OPERAND_IMAGE=$(jq -r '.[] | select(.name == "operand") | .image' /${RELATED_IMAGE_FILE}) && sed -i "s|${OPERAND_IMAGE_ORIGINAL}.*|${OPERAND_IMAGE}|g" /manifests/kueue-operator.clusterserviceversion.yaml
RUN MUSTGATHER_IMAGE=$(jq -r '.[] | select(.name == "must-gather") | .image' /${RELATED_IMAGE_FILE}) && sed -i "s|${MUSTGATHER_IMAGE_ORIGINAL}.*|${MUSTGATHER_IMAGE}|g" /manifests/kueue-operator.clusterserviceversion.yaml

FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=kueue-operator
LABEL operators.operatorframework.io.bundle.channels.v1=alpha
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.37.0
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v4

LABEL io.k8s.display-name="Red Hat build of Kueue Operator bundle"
LABEL io.k8s.description="This is a bundle for the Red Hat build of Kueue Operator"
LABEL com.redhat.component="kueue-operator-bundle"
LABEL com.redhat.openshift.versions="v4.18-v4.19"
LABEL name="kueue-operator-rhel9-operator-bundle"
LABEL summary="kueue-operator-bundle"
LABEL url="https://github.com/openshift/kueue-operator"
LABEL vendor="Red Hat, Inc."
LABEL io.openshift.expose-services=""
LABEL io.openshift.tags="openshift,kueue-operator-bundle"
LABEL description="kueue-operator-bundle"
LABEL distribution-scope="public"
LABEL release=1.1.0
LABEL version=1.1.0

LABEL maintainer="Node team, <aos-node@redhat.com>"

# Copy files to locations specified by labels.
COPY bundle/manifests /manifests/
COPY bundle/metadata /metadata/

COPY --from=builder manifests/kueue-operator.clusterserviceversion.yaml /manifests/kueue-operator.clusterserviceversion.yaml

# licenses required by Red Hat certification policy
# refer to https://docs.redhat.com/en/documentation/red_hat_software_certification/2024/html-single/red_hat_openshift_software_certification_policy_guide/index#con-image-content-requirements_openshift-sw-cert-policy-container-images
COPY LICENSE /licenses/

USER 1001
