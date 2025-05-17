FROM quay.io/openshift/origin-must-gather:latest as builder

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.6-1747218906

RUN microdnf update -y && microdnf install tar rsync bash -y && microdnf clean all

COPY --from=builder /usr/bin/oc /usr/bin/oc
COPY --from=builder /usr/bin/gather /usr/bin/gather_original

# COPY must-gather/collection-scripts/gather /gather
# COPY must-gather/collection-scripts/gather-kueue.sh /gather-kueue.sh

COPY must-gather/collection-scripts/gather /usr/bin/gather
COPY must-gather/collection-scripts/gather-kueue /usr/bin/gather-kueue

# RUN chmod +x /gather /gather-kueue.sh
RUN chmod +x /usr/bin/gather /usr/bin/gather-kueue

# ENTRYPOINT /usr/bin/gather
# ENTRYPOINT ["/gather"]
CMD ["/bin/bash", "/usr/bin/gather"]