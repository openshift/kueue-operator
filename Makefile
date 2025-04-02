all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/deps.mk \
)

# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/...
GO_BUILD_FLAGS :=-tags strictfipsruntime

IMAGE_REGISTRY ?=registry.svc.ci.openshift.org

OPERATOR_VERSION ?= 0.0.1
# These are targets for pushing images
OPERATOR_IMAGE ?= mustchange
BUNDLE_IMAGE ?= mustchange
KUEUE_IMAGE ?= mustchange

NAMESPACE ?= openshift-kueue-operator
KUBECONFIG ?= ${HOME}/.kube/config

CONTAINER_TOOL ?= podman

CODEGEN_OUTPUT_PACKAGE :=github.com/openshift/kueue-operator/pkg/generated
CODEGEN_API_PACKAGE :=github.com/openshift/kueue-operator/pkg/apis
CODEGEN_GROUPS_VERSION :=kueue:v1alpha1

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

$(call verify-golang-versions,Dockerfile)

$(call add-crd-gen,kueueoperator,./pkg/apis/kueueoperator/v1alpha1,./manifests/,./manifests/)

.PHONY: test-e2e
test-e2e: ginkgo
	${GINKGO} -v ./test/e2e/...

regen-crd:
	go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen crd paths=./pkg/apis/kueueoperator/v1alpha1/... schemapatch:manifests=./manifests output:crd:dir=./manifests
	cp manifests/operator.openshift.io_kueues.yaml manifests/kueue-operator.crd.yaml
	cp manifests/kueue-operator.crd.yaml deploy/crd/kueue-operator.crd.yaml
	cp deploy/crd/kueue-operator.crd.yaml test/e2e/bindata/assets/00_kueue-operator.crd.yaml

.PHONY: generate
generate: manifests code-gen generate-clients

.PHONY: manifests
manifests: ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen crd paths=./pkg/apis/kueueoperator/v1alpha1/... schemapatch:manifests=./manifests output:crd:dir=./manifests

.PHONY: code-gen
code-gen: ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	go run ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: generate-clients
generate-clients:
	GO=GO111MODULE=on GOTOOLCHAIN=go1.23.4 GOFLAGS=-mod=readonly hack/update-codegen.sh

.PHONY: bundle-generate
bundle-generate: operator-sdk regen-crd manifests
	hack/update-deploy-files.sh ${OPERATOR_IMAGE} ${KUEUE_IMAGE}
	${OPERATOR_SDK} generate bundle --input-dir deploy/ --version ${OPERATOR_VERSION}
	hack/revert-deploy-files.sh ${OPERATOR_IMAGE} ${KUEUE_IMAGE}

.PHONY: deploy-ocp
deploy-ocp:
	hack/update-deploy-files.sh ${OPERATOR_IMAGE} ${KUEUE_IMAGE}
	oc apply -f deploy/
	oc apply -f deploy/crd/
	oc apply -f deploy/examples/job.yaml
	hack/revert-deploy-files.sh ${OPERATOR_IMAGE} ${KUEUE_IMAGE}

.PHONY: undeploy-ocp
undeploy-ocp:
	hack/undeploy-ocp.sh

.PHONY: deploy-cert-manager
deploy-cert-manager:
	hack/deploy-cert-manager.sh

.PHONY: deploy-upstream-cert-manager
deploy-upstream-cert-manager:
	kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.0/cert-manager.yaml

# Below targets require you to login to your registry
.PHONY: operator-build
operator-build:
	${CONTAINER_TOOL} build -f Dockerfile -t ${OPERATOR_IMAGE}

.PHONY: operator-push
operator-push:
	${CONTAINER_TOOL} push ${OPERATOR_IMAGE}

# Below targets require you to login to your registry
.PHONY: bundle-build
bundle-build: bundle-generate
	${CONTAINER_TOOL} build -f bundle.Dockerfile -t ${BUNDLE_IMAGE}

.PHONY: bundle-push
bundle-push:
	${CONTAINER_TOOL} push ${BUNDLE_IMAGE}

clean:
	$(RM) ./kueue-operator
	$(RM) -r ./_tmp
.PHONY: clean

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter 
	$(GOLANGCI_LINT) run --timeout 30m

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix --timeout 30m

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.17.1

GOLANGCI_LINT = $(shell pwd)/bin/golangci-lint
GOLANGCI_LINT_VERSION ?= v1.61.0
golangci-lint:
	@[ -f $(GOLANGCI_LINT) ] || { \
	set -e ;\
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell dirname $(GOLANGCI_LINT)) $(GOLANGCI_LINT_VERSION) ;\
	}

.PHONY: operator-sdk
OPERATOR_SDK ?= $(LOCALBIN)/operator-sdk
operator-sdk: ## Download operator-sdk locally if necessary.
ifeq (,$(wildcard $(OPERATOR_SDK)))
ifeq (, $(shell which operator-sdk 2>/dev/null))
	@{ \
	set -e ;\
	mkdir -p $(dir $(OPERATOR_SDK)) ;\
	OS=$(shell go env GOOS) && ARCH=$(shell go env GOARCH) && \
	curl -sSLo $(OPERATOR_SDK) https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk_$${OS}_$${ARCH} ;\
	chmod +x $(OPERATOR_SDK) ;\
	}
else
OPERATOR_SDK = $(shell which operator-sdk)
endif
endif

# Use this target like make sync-manifests VERSION=<version>
.PHONY: sync-manifests
sync-manifests:
	@echo "Syncing manifests in bindata/assets/kueue-operator using a container"
	@podman run --rm \
		-v $(PWD):/workspace:Z \
		-w /workspace \
		python:3.11-slim \
		sh -c " \
			echo 'Checking for Python dependencies...'; \
			pip install pyyaml requests > /dev/null; \
			echo 'Running sync_manifests.py...'; \
			python3 hack/sync_manifests.py $(VERSION) \
		"
GINKGO = $(shell pwd)/bin/ginkgo
.PHONY: ginkgo
ginkgo: ## Download ginkgo locally if necessary.
	GOBIN=$(LOCALBIN) GO111MODULE=on go install -mod=mod github.com/onsi/ginkgo/v2/ginkgo@v2.1.4
