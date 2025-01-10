all: build
.PHONY: all

# Include the library makefile
include $(addprefix ./vendor/github.com/openshift/build-machinery-go/make/, \
	golang.mk \
	targets/openshift/images.mk \
	targets/openshift/deps.mk \
)

# Exclude e2e tests from unit testing
GO_TEST_PACKAGES :=./pkg/...
GO_BUILD_FLAGS :=-tags strictfipsruntime

IMAGE_REGISTRY :=registry.svc.ci.openshift.org

CODEGEN_OUTPUT_PACKAGE :=github.com/openshift/kueue-operator/pkg/generated
CODEGEN_API_PACKAGE :=github.com/openshift/kueue-operator/pkg/apis
CODEGEN_GROUPS_VERSION :=kueue:v1alpha1

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

# This will call a macro called "build-image" which will generate image specific targets based on the parameters:
# $0 - macro name
# $1 - target name
# $2 - image ref
# $3 - Dockerfile path
# $4 - context directory for image build
$(call build-image,ocp-kueue-operator,$(IMAGE_REGISTRY)/ocp/4.19:kueue-operator, ./Dockerfile,.)

$(call verify-golang-versions,Dockerfile)

$(call add-crd-gen,kueueoperator,./pkg/apis/kueueoperator/v1alpha1,./manifests/,./manifests/)

test-e2e: GO_TEST_PACKAGES :=./test/e2e
# the e2e imports pkg/cmd which has a data race in the transport library with the library-go init code
test-e2e: GO_TEST_FLAGS :=-v
test-e2e: test-unit
.PHONY: test-e2e

regen-crd:
	go build -o $(LOCALBIN)/controller-gen ./vendor/sigs.k8s.io/controller-tools/cmd/controller-gen
	$(LOCALBIN)/controller-gen crd paths=./pkg/apis/kueueoperator/v1alpha1/... schemapatch:manifests=./manifests output:crd:dir=./manifests
	mv manifests/operator.openshift.io_kueues.yaml manifests/kueue-operator.crd.yaml
	rm deploy/crd/kueue-operator.crd.yaml
	cp manifests/kueue-operator.crd.yaml deploy/crd/kueue-operator.crd.yaml

.PHONY: generate
generate: manifests code-gen generate-clients

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) crd paths=./pkg/apis/kueueoperator/v1alpha1/... schemapatch:manifests=./manifests output:crd:dir=./manifests

.PHONY: code-gen
code-gen: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: generate-clients
generate-clients:
	GO=GO111MODULE=on GOTOOLCHAIN=go1.23.4 GOFLAGS=-mod=readonly hack/update-codegen.sh

clean:
	$(RM) ./kueue-operator
	$(RM) -r ./_tmp
.PHONY: clean

## Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.17.1

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary. If wrong version is installed, it will be overwritten.
$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)


