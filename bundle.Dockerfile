FROM scratch

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=kueue-operator
LABEL operators.operatorframework.io.bundle.channels.v1=alpha
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.37.0
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v4

LABEL io.k8s.display-name="OpenShift Kueue Bundle" \
      io.k8s.description="This is a bundle for the kueue operator" \
      com.redhat.component="kueue-operator-bundle" \
      com.redhat.openshift.versions="v4.17-v4.18" \
      name="kueue-operator-rhel9-operator-bundle" \
      summary="kueue-operator-bundle" \
      url="https://github.com/openshift/kueue-operator" \
      vendor="Red Hat, Inc." \
      io.openshift.expose-services="" \
      io.openshift.tags="openshift,kueue-operator-bundle" \
      description="kueue-operator-bundle" \
      distribution-scope="public" \
      release=0.0.1 \
      maintainer="Node team, <aos-node@redhat.com>"


# Copy files to locations specified by labels.
COPY bundle/manifests /manifests/
COPY bundle/metadata /metadata/

# licenses required by Red Hat certification policy
# refer to https://docs.redhat.com/en/documentation/red_hat_software_certification/2024/html-single/red_hat_openshift_software_certification_policy_guide/index#con-image-content-requirements_openshift-sw-cert-policy-container-images
COPY LICENSE /licenses/
