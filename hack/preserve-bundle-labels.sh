#!/bin/bash
set -exou pipefail

KUEUE_OPERAND_IMAGE="registry.redhat.io/kueue-tech-preview/kueue-rhel9@sha256:dd765c446564d4397570f4bc056b55989f6c0bae0d70514874e67765c9c77889"
KUEUE_OPERATOR_IMAGE="registry.redhat.io/kueue-tech-preview/kueue-rhel9-operator@sha256:0c4d9cd97f7579adbf2afd238c953ca5f170408cf27e3b2bf4aa16c108a76881"
DESIRED_BASE="scratch"
CSV_FILE="bundle/manifests/kueue-operator.clusterserviceversion.yaml"
DOCKERFILE="bundle.Dockerfile"
DISPLAY_NAME="Red Hat build of Kueue"
B64_ICON='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'
ICON_TYPE='image/png'
DESCRIPTION="Red Hat build of Kueue is a collection of APIs, based on the [Kueue](https://kueue.sigs.k8s.io/docs/) open source project, that extends Kubernetes to manage access to resources for jobs. Red Hat build of Kueue does not replace any existing components in a Kubernetes cluster, but instead integrates with the existing Kubernetes API server, scheduler, and cluster autoscaler components to determine when a job waits, is admitted to start by creating pods, or should be preempted."

# Use the correct base image for the bundle.
sed -i "s|^FROM .*|FROM ${DESIRED_BASE}|" "${DOCKERFILE}"

# Insert custom labels after metrics.project_layout label.
sed -i "/^LABEL operators.operatorframework.io.metrics.project_layout=go\.kubebuilder\.io\/v4/a \\
\\
LABEL io.k8s.display-name=\"Red Hat build of Kueue Operator bundle\"\\
LABEL io.k8s.description=\"This is a bundle for the Red Hat build of Kueue Operator\"\\
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
LABEL release=1.1.0\\
LABEL version=1.1.0\\
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

# Add the icon to the CSV.
sed -i -e "s|base64data: \"\"|base64data: \"${B64_ICON}\"|" \
       -e "s|mediatype: \"\"|mediatype: \"${ICON_TYPE}\"|" \
       "${CSV_FILE}"

# Replace image references.
sed -i "s|value: mustchange|value: ${KUEUE_OPERAND_IMAGE}|g" "${CSV_FILE}"
sed -i "s|image: mustchange|image: ${KUEUE_OPERATOR_IMAGE}|g" "${CSV_FILE}"

# Replace Display Name
sed -i "s|displayName: Kueue Operator|displayName: ${DISPLAY_NAME}|" "${CSV_FILE}"

# Replace Description
sed -i "s|description: Kueue Operator description. TODO.|description: ${DESCRIPTION}|" "${CSV_FILE}"

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

# Add the relatedImages entries
yq -i '
  .spec.relatedImages += [
    {"name": "must-gather", "image": env(MUST_GATHER_IMAGE)}
  ]
' "${CSV_FILE}"

# Add/update minKubeVersion.
if ! grep -q "minKubeVersion" "${CSV_FILE}"; then
  sed -i '/version: 1.1.0/a \  minKubeVersion: 1.28.0' "${CSV_FILE}"
else
  sed -i 's/minKubeVersion:.*/minKubeVersion: 1.28.0/g' "${CSV_FILE}"
fi

# Ensure .metadata.labels is preserved to support multiarchs
yq -i '
  .metadata.labels = (.metadata.labels // {}) |
  .metadata.labels["operatorframework.io/arch.amd64"] = "supported" |
  .metadata.labels["operatorframework.io/arch.arm64"] = "supported"
' "$CSV_FILE"