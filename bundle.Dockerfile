FROM registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.23-openshift-4.19 as builder
WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .

RUN mkdir licenses
COPY ./LICENSE licenses/.

ARG OPERATOR_IMAGE=registry.redhat.io/openshift-kueue-operator/kueue-operator-rhel9-operator@sha256:21e561aa5f8eb27b1da033acff96baba574335fd56aeb2bd2b5c28626735bb5c
ARG REPLACED_OPERATOR_IMG=registry-proxy.engineering.redhat.com/rh-osbs/kueue-rhel9-operator:latest

RUN hack/replace-image.sh manifests ${REPLACED_OPERATOR_IMG} ${OPERATOR_IMAGE}

FROM registry.redhat.io/rhel9-4-els/rhel-minimal:9.4

COPY --from=builder /go/src/github.com/openshift/kueue-operator/manifests /manifests
COPY --from=builder /go/src/github.com/openshift/kueue-operator/metadata /metadata
COPY --from=builder /go/src/github.com/openshift/kueue-operator/licenses /licenses

LABEL operators.operatorframework.io.bundle.mediatype.v1="registry+v1"
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1="openshift-kueue-operator"
LABEL operators.operatorframework.io.bundle.channels.v1=stable
LABEL operators.operatorframework.io.bundle.channel.default.v1=stable
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.34.2
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v4

LABEL com.redhat.component="kueue-operator-bundle-container"
LABEL description="Kueue support for OpenShift"
LABEL distribution-scope="public"
LABEL name="kueue-operator-metadata-rhel-9"
LABEL release="1.4.0"
LABEL version="1.4.0"
LABEL url="https://github.com/openshift/kueue-operator"
LABEL vendor="Red Hat, Inc."
LABEL summary="Kueue support for OpenShift"
LABEL io.openshift.expose-services=""
LABEL io.k8s.display-name="Openshift Kueue Operator Bundle"
LABEL io.k8s.description="This is a bundle image for Kueue Operator"
LABEL io.openshift.tags="openshift,kueue-operator"
LABEL com.redhat.delivery.operator.bundle=true
LABEL com.redhat.openshift.versions="v4.16"
LABEL com.redhat.delivery.appregistry=true
LABEL maintainer="Node team, <aos-node@redhat.com>"

USER 1001
