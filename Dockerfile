FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.26 as builder
WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.redhat.io/ubi9/ubi-minimal@sha256:2e8edce823a48e51858f1fad3ff4cbf6875ce8a3f86b9eecf298bc2050c8652a
RUN microdnf install -y lsof && microdnf clean all && rm -rf /var/cache/{dnf,yum}
COPY --from=builder /go/src/github.com/openshift/kueue-operator/kueue-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/kueue-operator/LICENSE /licenses/.

LABEL io.k8s.display-name="Red Hat Build of Kueue Operator based on RHEL 9"
LABEL io.k8s.description="This is a component of OpenShift and manages kueue based on RHEL 9"
LABEL com.redhat.component="kueue-operator-container"
LABEL com.redhat.openshift.versions="v4.18-v4.19-v4.20-v4.21-v4.22"
LABEL summary="kueue-operator"
LABEL url="https://github.com/openshift/kueue-operator"
LABEL io.openshift.expose-services=""
LABEL io.openshift.tags="openshift,kueue-operator"
LABEL description="kueue-operator-container"
LABEL distribution-scope="public"
LABEL name="kueue-operator-rhel9-operator"
LABEL cpe="cpe:/a:redhat:kueue_operator:1.5::el9"
LABEL vendor="Red Hat, Inc."
LABEL version=1.5.0
LABEL release=1.5.0
LABEL maintainer="Node team, <aos-node@redhat.com>"

USER 1001
