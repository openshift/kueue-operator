RELEASE ?= main

#include Makefile-deps.mk
#include Makefile-test-ocp.mk

all:
	make -C ${RELEASE}/src

.PHONY: image-build-generic
image-build-generic:
	$(IMAGE_BUILD_CMD) -t $(IMAGE_TAG) \
		$(IMAGE_BUILD_EXTRA_OPTS) ./

.PHONY: image-push-generic
image-push-generic:
	$(DOCKER_BUILDX_CMD) push $(IMAGE_TAG)
