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
      name="kueue-operator-rhel9-operator-bundle" \
      summary="kueue-operator-bundle" \
      io.openshift.expose-services="" \
      io.openshift.tags="openshift,kueue-operator-bundle" \
      description="kueue-operator-bundle" \
      maintainer="Node team, <aos-node@redhat.com>"


# Copy files to locations specified by labels.
COPY bundle/manifests /manifests/
COPY bundle/metadata /metadata/
