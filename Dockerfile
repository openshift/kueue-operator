FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.23 as builder
WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.redhat.io/rhel9-4-els/rhel-minimal@sha256:7a71fea601daf5a7d3be2347b83017c074a8b8b6aef22877e27a61fe23395f7f
COPY --from=builder /go/src/github.com/openshift/kueue-operator/kueue-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/kueue-operator/LICENSE /licenses/.

LABEL io.k8s.display-name="OpenShift Kueue Operator based on RHEL 9" \
      io.k8s.description="This is a component of OpenShift and manages kueue based on RHEL 9" \
      com.redhat.component="kueue-operator-container" \
      com.redhat.openshift.versions="v4.17-v4.18" \
      name="kueue-operator-rhel9-operator" \
      summary="kueue-operator" \
      url="https://github.com/openshift/kueue-operator" \
      vendor="Red Hat, Inc." \
      io.openshift.expose-services="" \
      io.openshift.tags="openshift,kueue-operator" \
      description="kueue-operator-container" \
      distribution-scope="public" \
      version="0.0.1" \
      maintainer="Node team, <aos-node@redhat.com>"

USER 1001
