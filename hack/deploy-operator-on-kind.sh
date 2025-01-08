#!/usr/bin/env bash

set -eou pipefail

KIND=${KIND:-kind}
KUBECTL=${KUBECTL:-kubectl}
KIND_NAME=${KIND_NAME:-"kind-e2e"}
KIND_CONTEXT=kind-${KIND_NAME}
NAMESPACE=${NAMESPACE:-"kueue-operator"}
KIND_NODE_NAME=${KIND_NODE_NAME:-"kind-e2e-control-plane"}
WEBHOOK_TIMEOUT=${WEBHOOK_TIMEOUT:-2m}

_kubectl() {
        ${KUBECTL} --context ${KIND_CONTEXT} $@
}

_kind() {
	${KIND} $@
}

_kind load docker-image ${IMG} --name ${KIND_NAME}
echo "Installing kueue operator CRD"
make install
sleep 10
${KUBECTL} config set-context ${KIND_CONTEXT}
echo "Deploying Kueue Operator controller-manager"
make deploy
_kubectl wait --for=condition=ready pod -l control-plane=controller-manager -n ${NAMESPACE} --timeout=${WEBHOOK_TIMEOUT}
