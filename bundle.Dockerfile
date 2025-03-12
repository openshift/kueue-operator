FROM registry.access.redhat.com/ubi9/ubi-micro@sha256:323c3057faad403b79c2866a0c4efcba6b15ad85ee9afe169fea5b73d0746b82

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=kueue-operator
LABEL operators.operatorframework.io.bundle.channels.v1=alpha
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.37.0
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v4

LABEL io.k8s.display-name="OpenShift Kueue Bundle"
LABEL io.k8s.description="This is a bundle for the kueue operator"
LABEL com.redhat.component="kueue-operator-bundle"
LABEL com.redhat.openshift.versions="v4.17-v4.18"
LABEL name="kueue-operator-rhel9-operator-bundle"
LABEL summary="kueue-operator-bundle"
LABEL url="https://github.com/openshift/kueue-operator"
LABEL vendor="Red Hat, Inc."
LABEL io.openshift.expose-services=""
LABEL io.openshift.tags="openshift,kueue-operator-bundle"
LABEL description="kueue-operator-bundle"
LABEL distribution-scope="public"
LABEL release=0.0.1
LABEL version=0.0.1

LABEL maintainer="Node team, <aos-node@redhat.com>"

# Copy files to locations specified by labels.
COPY bundle/manifests /manifests/
COPY bundle/metadata /metadata/

# licenses required by Red Hat certification policy
# refer to https://docs.redhat.com/en/documentation/red_hat_software_certification/2024/html-single/red_hat_openshift_software_certification_policy_guide/index#con-image-content-requirements_openshift-sw-cert-policy-container-images
COPY LICENSE /licenses/

USER 1001
