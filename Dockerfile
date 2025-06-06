FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.23 as builder
WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.access.redhat.com/ubi9/ubi-micro@sha256:955512628a9104d74f7b3b0a91db27a6bbecdd6a1975ce0f1b2658d3cd060b98
COPY --from=builder /go/src/github.com/openshift/kueue-operator/kueue-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/kueue-operator/LICENSE /licenses/.

LABEL io.k8s.display-name="OpenShift Kueue Operator based on RHEL 9"
LABEL io.k8s.description="This is a component of OpenShift and manages kueue based on RHEL 9"
LABEL com.redhat.component="kueue-operator-container"
LABEL com.redhat.openshift.versions="v4.17-v4.18"
LABEL summary="kueue-operator"
LABEL url="https://github.com/openshift/kueue-operator"
LABEL io.openshift.expose-services=""
LABEL io.openshift.tags="openshift,kueue-operator"
LABEL description="kueue-operator-container"
LABEL distribution-scope="public"
LABEL name="kueue-operator-rhel9-operator"
LABEL vendor="Red Hat, Inc."
LABEL version=0.0.1
LABEL release=0.0.1
LABEL maintainer="Node team, <aos-node@redhat.com>"

USER 1001
