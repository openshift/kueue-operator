#!/bin/bash
set -exou pipefail

KUEUE_OPERAND_IMAGE="quay.io/redhat-user-workloads/kueue-operator-tenant/kubernetes-sigs-kueue@sha256:ec6688b483a2919d10d7ff6876f559c7b98961c6da425d80a653f0fe55db684d"
KUEUE_OPERATOR_IMAGE="quay.io/redhat-user-workloads/kueue-operator-tenant/kueue-operator@sha256:c675b15b873f26e890784c9fc40ffc2e3dbee701f8b6b719a1d4247f8d127d32"
DESIRED_BASE="registry.access.redhat.com/ubi9/ubi-micro@sha256:d086e9b85efa3818f9429c2959c9acd62a6a4115c7ad6d59ae428c61d3c704fa"
CSV_FILE="bundle/manifests/kueue-operator.clusterserviceversion.yaml"
DOCKERFILE="bundle.Dockerfile"

# Use the correct base image for the bundle.
sed -i "s|^FROM .*|FROM ${DESIRED_BASE}|" "${DOCKERFILE}"

# Insert custom labels after metrics.project_layout label.
sed -i "/^LABEL operators.operatorframework.io.metrics.project_layout=go\.kubebuilder\.io\/v4/a \\
\\
LABEL io.k8s.display-name=\"OpenShift Kueue Bundle\"\\
LABEL io.k8s.description=\"This is a bundle for the kueue operator\"\\
LABEL com.redhat.component=\"kueue-operator-bundle\"\\
LABEL com.redhat.openshift.versions=\"v4.18-v4.19\"\\
LABEL name=\"kueue-operator-rhel9-operator-bundle\"\\
LABEL summary=\"kueue-operator-bundle\"\\
LABEL url=\"https://github.com/openshift/kueue-operator\"\\
LABEL vendor=\"Red Hat, Inc.\"\\
LABEL io.openshift.expose-services=\"\"\\
LABEL io.openshift.tags=\"openshift,kueue-operator-bundle\"\\
LABEL description=\"kueue-operator-bundle\"\\
LABEL distribution-scope=\"public\"\\
LABEL release=0.0.1\\
LABEL version=0.0.1\\
\\
LABEL maintainer=\"Node team, <aos-node@redhat.com>\"" "${DOCKERFILE}"

# Add license and user instructions.
sed -i "/^COPY bundle\/metadata /a \\
\\
# licenses required by Red Hat certification policy\\
# refer to https://docs.redhat.com/en/documentation/red_hat_software_certification/2024/html-single/red_hat_openshift_software_certification_policy_guide/index#con-image-content-requirements_openshift-sw-cert-policy-container-images\\
COPY LICENSE \/licenses\/\\
\\
USER 1001" "${DOCKERFILE}"

# Add required annotations after project_layout line
sed -i '/operators.operatorframework.io\/project_layout: go.kubebuilder.io\/v4/a \
    console.openshift.io\/operator-monitoring-default: "true"\
    features.operators.openshift.io\/cnf: "false"\
    features.operators.openshift.io\/cni: "false"\
    features.operators.openshift.io\/csi: "false"\
    features.operators.openshift.io\/disconnected: "true"\
    features.operators.openshift.io\/fips-compliant: "true"\
    features.operators.openshift.io\/proxy-aware: "false"\
    features.operators.openshift.io\/tls-profiles: "false"\
    features.operators.openshift.io\/token-auth-aws: "false"\
    features.operators.openshift.io\/token-auth-azure: "false"\
    features.operators.openshift.io\/token-auth-gcp: "false"\
    operatorframework.io\/cluster-monitoring: "true"\
    operatorframework.io\/suggested-namespace: openshift-kueue-operator\
    operators.openshift.io\/valid-subscription: '\''["OpenShift Kubernetes Engine", "OpenShift Container Platform", "OpenShift Platform Plus"]'\''\
    operators.operatorframework.io\/builder: operator-sdk-v1.33.0' "${CSV_FILE}"

# Replace image references.
sed -i "s|value: mustchange|value: ${KUEUE_OPERAND_IMAGE}|g" "${CSV_FILE}"
sed -i "s|image: mustchange|image: ${KUEUE_OPERATOR_IMAGE}|g" "${CSV_FILE}"

# Update links URL.
sed -i 's|url: https://kueue-operator.domain|url: https://github.com/openshift/kueue-operator|g' "${CSV_FILE}"

# Fix maintainers section (removes duplicates)
sed -i '/maintainers:/,/^  maturity:/ {/maintainers:/{n;d}; /^  - email: your@email.com/,/^  maturity:/d}' "${CSV_FILE}"
sed -i '/maintainers:/a \  - email: aos-node@redhat.com\n    name: Node team' "${CSV_FILE}"

# Remove any remaining duplicate names.
sed -i '/name: Node team/{n;/name:/d}' "${CSV_FILE}"

# Update provider information.
sed -i 's|name: Provider Name|name: Red Hat, Inc|' "${CSV_FILE}"
sed -i 's|url: https://your.domain|url: https://github.com/openshift/kueue-operator|' "${CSV_FILE}"

# Fix relatedImages entry.
sed -i "/relatedImages:/{n;s|image:.*|image: ${KUEUE_OPERAND_IMAGE}|;n;s|name:.*|name: operand-image|}" "${CSV_FILE}"

# Add/update minKubeVersion.
if ! grep -q "minKubeVersion" "${CSV_FILE}"; then
  sed -i '/version: 0.0.1/a \  minKubeVersion: 1.28.0' "${CSV_FILE}"
else
  sed -i 's/minKubeVersion:.*/minKubeVersion: 1.28.0/g' "${CSV_FILE}"
fi
