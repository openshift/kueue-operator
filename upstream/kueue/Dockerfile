FROM brew.registry.redhat.io/rh-osbs/openshift-golang-builder:rhel_9_1.24 AS build

ARG TARGETOS
ARG TARGETARCH
ARG TARGETPLATFORM

WORKDIR /workspace
COPY src/ .
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -mod=vendor -a -o manager cmd/kueue/main.go

FROM registry.redhat.io/ubi9/ubi-minimal@sha256:2f06ae0e6d3d9c4f610d32c480338eef474867f435d8d28625f2985e8acde6e8
WORKDIR /
COPY --from=build /workspace/manager /
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

COPY --from=build /workspace/LICENSE /licenses/LICENSE

ENTRYPOINT ["/manager"]
