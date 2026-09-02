FROM registry.redhat.io/openshift4/ose-must-gather-rhel9@sha256:a4e9c44342e2b175dc9b7fedd451b9bfd628729f0709509873b8c2e9c2c9de46 as builder

FROM registry.redhat.io/ubi9/ubi-minimal-pqc@sha256:3e009398a8aa8eec621393fbf308c5e622f174900e44e8d5fe224c637920924a

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
LABEL release=1.4.2
LABEL version=1.4.2
LABEL maintainer="Node team, <aos-node@redhat.com>"
