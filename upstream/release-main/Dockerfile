ARG BUILDER_IMAGE=brew.registry.redhat.io/rh-osbs/openshift-golang-builder:v1.24
ARG BASE_IMAGE=registry.redhat.io/ubi9/ubi-minimal@sha256:295f920819a6d05551a1ed50a6c71cb39416a362df12fa0cd149bc8babafccff

# Build the manager binary
FROM ${BUILDER_IMAGE} AS builder

ARG TARGETOS
ARG TARGETARCH
ARG TARGETPLATFORM

ENV GOEXPERIMENT=strictfipsruntime

WORKDIR /workspace
COPY upstream/release-main/src/ .

RUN CGO_ENABLED=1 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -tags strictfipsruntime -mod=vendor -a -o manager cmd/kueue/main.go

FROM --platform=${TARGETPLATFORM} ${BASE_IMAGE}
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

LABEL com.redhat.component="Kueue"
LABEL name="kueue"
LABEL url="https://github.com/openshift/kubernetes-sigs-kueue"
LABEL vendor="Red Hat, Inc."
LABEL description="Kueue is a set of APIs and controller for job queueing. \
                    It is a job-level manager that decides when a job should \ 
                    be admitted to start (as in pods can be created) \
                    and when it should stop (as in active pods should be deleted)."
LABEL io.k8s.description="Kueue is a set of APIs and controller for job queueing. \
                    It is a job-level manager that decides when a job should \
                    be admitted to start (as in pods can be created) \
                    and when it should stop (as in active pods should be deleted)."
LABEL summary="Kueue is a set of APIs and controller for job queueing. \
                    It is a job-level manager that decides when a job should \
                    be admitted to start (as in pods can be created) \
                    and when it should stop (as in active pods should be deleted)."
LABEL io.k8s.display-name="Kueue"
LABEL io.openshift.tags="openshift,operand,kueue"

# Licenses

COPY LICENSE /licenses/LICENSE

ENTRYPOINT ["/manager"]
