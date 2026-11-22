FROM registry.redhat.io/rhel9/go-toolset:1.27 AS builder
# go-toolset runs as UID 1001; the build writes into the root-owned COPY tree.
USER 0
WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.redhat.io/ubi9/ubi-minimal-pqc@sha256:5e9a0970f73f4800445c3ce0ca0d6b7ea1a8c620b81e735d16432db09d6787bc
COPY --from=builder /go/src/github.com/openshift/kueue-operator/kueue-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/kueue-operator/LICENSE /licenses/.

LABEL io.k8s.display-name="Red Hat Build of Kueue Operator based on RHEL 9"
LABEL io.k8s.description="This is a component of OpenShift and manages kueue based on RHEL 9"
LABEL com.redhat.component="kueue-operator-container"
LABEL com.redhat.openshift.versions="v4.18-v4.19-v4.20-v4.21-v4.22-v4.23-v5.0"
LABEL summary="kueue-operator"
LABEL url="https://github.com/openshift/kueue-operator"
LABEL io.openshift.expose-services=""
LABEL io.openshift.tags="openshift,kueue-operator"
LABEL description="kueue-operator-container"
LABEL distribution-scope="public"
LABEL name="kueue-operator-rhel9-operator"
LABEL cpe="cpe:/a:redhat:kueue_operator:1.6::el9"
LABEL vendor="Red Hat, Inc."
LABEL version=1.6.0
LABEL release=1.6.0
LABEL maintainer="Node team, <aos-node@redhat.com>"

USER 1001
