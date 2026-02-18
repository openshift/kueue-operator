FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.25 as builder
WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.redhat.io/ubi9/ubi-minimal@sha256:c7d44146f826037f6873d99da479299b889473492d3c1ab8af86f08af04ec8a0
COPY --from=builder /go/src/github.com/openshift/kueue-operator/kueue-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/kueue-operator/LICENSE /licenses/.

LABEL io.k8s.display-name="Red Hat Build of Kueue Operator based on RHEL 9"
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
LABEL cpe="cpe:/a:redhat:kueue_operator:1.3::el9"
LABEL vendor="Red Hat, Inc."
LABEL version=1.3.0
LABEL release=1.3.0
LABEL maintainer="Node team, <aos-node@redhat.com>"

USER 1001
