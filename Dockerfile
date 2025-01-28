FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.23 as builder
WORKDIR /go/src/github.com/openshift/kueue-operator
COPY . .
RUN make build --warn-undefined-variables

FROM registry.redhat.io/rhel9-4-els/rhel-minimal:9.4-12
COPY --from=builder /go/src/github.com/openshift/kueue-operator/kueue-operator /usr/bin/
RUN mkdir /licenses
COPY --from=builder /go/src/github.com/openshift/kueue-operator/LICENSE /licenses/.

LABEL io.k8s.display-name="OpenShift Kueue Operator based on RHEL 9" \
      io.k8s.description="This is a component of OpenShift and manages kueue based on RHEL 9" \
      com.redhat.component="kueue-operator-container" \
      name="kueue-operator-rhel9-operator" \
      summary="kueue-operator" \
      io.openshift.expose-services="" \
      io.openshift.tags="openshift,kueue-operator" \
      description="kueue-operator-container" \
      maintainer="Node team, <aos-node@redhat.com>"

USER nobody
