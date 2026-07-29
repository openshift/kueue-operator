FROM registry.redhat.io/openshift4/ose-must-gather-rhel9@sha256:1101c3491186096e60ad91c2bd8bc1978622cbc018750312f810616af0928570 as builder

FROM registry.redhat.io/ubi9/ubi-minimal@sha256:2a98ec380ad75004992b8538380a5003da64442f0a69237ed236859e023c71d0

COPY --from=builder /usr/bin/oc /usr/bin/oc
COPY --from=builder /usr/bin/gather /usr/bin/gather_original
COPY --from=builder /usr/bin/rsync /usr/bin/rsync
COPY --from=builder /usr/bin/tar /usr/bin/tar

# COPY must-gather/collection-scripts/gather /gather
# COPY must-gather/collection-scripts/gather-kueue.sh /gather-kueue.sh

COPY must-gather/collection-scripts/gather /usr/bin/gather
COPY must-gather/collection-scripts/gather-kueue /usr/bin/gather-kueue

# RUN chmod +x /gather /gather-kueue.sh
RUN chmod +x /usr/bin/gather /usr/bin/gather-kueue

# ENTRYPOINT /usr/bin/gather
# ENTRYPOINT ["/gather"]
CMD ["/bin/bash", "/usr/bin/gather"]

LABEL io.k8s.display-name="OpenShift Kueue must-gather"
LABEL io.k8s.description="This is a must-gather for the Kueue"
LABEL com.redhat.component="kueue-must-gather"
LABEL com.redhat.openshift.versions="v4.18-v4.19-v4.20-v4.21-v4.22"
LABEL name="kueue-must-gather"
LABEL summary="kueue-must-gather"
LABEL url="https://github.com/openshift/kueue-operator"
LABEL vendor="Red Hat, Inc."
LABEL io.openshift.expose-services=""
LABEL io.openshift.tags="openshift,kueue-must-gather"
LABEL description="kueue-must-gather"
LABEL distribution-scope="public"
LABEL cpe="cpe:/a:redhat:kueue_operator:1.4::el9"
LABEL release=1.4.0
LABEL version=1.4.0
LABEL maintainer="Node team, <aos-node@redhat.com>"
