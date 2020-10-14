# Current Operator version
VERSION ?= 0.0.1
# Current Gatekeeper version
GATEKEEPER_VERSION ?= v3.1.1
# Default bundle image tag
BUNDLE_IMG ?= controller-bundle:$(VERSION)
# Options for 'bundle-build'
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

# Image URL to use all building/pushing image targets
IMG ?= gatekeeper-operator:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:trivialVersions=true"

GATEKEEPER_MANIFEST_DIR ?= config/gatekeeper

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Use the vendored directory
GOFLAGS = -mod=vendor

all: manager

# Run tests
# Set SKIP_FETCH_TOOLS=y to use tools in your own environment
ENVTEST_ASSETS_DIR=$(shell pwd)/testbin
test: generate fmt vet manifests
	mkdir -p ${ENVTEST_ASSETS_DIR}
	test -f ${ENVTEST_ASSETS_DIR}/setup-envtest.sh || curl -sSLo ${ENVTEST_ASSETS_DIR}/setup-envtest.sh https://raw.githubusercontent.com/kubernetes-sigs/controller-runtime/master/hack/setup-envtest.sh
	source ${ENVTEST_ASSETS_DIR}/setup-envtest.sh; fetch_envtest_tools $(ENVTEST_ASSETS_DIR); setup_envtest_env $(ENVTEST_ASSETS_DIR); GOFLAGS=$(GOFLAGS) go test ./... -coverprofile cover.out

# Build manager binary
manager: generate fmt vet manifests
	GOFLAGS=$(GOFLAGS) go build -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	GOFLAGS=$(GOFLAGS) go run ./main.go

# Install CRDs into a cluster
install: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests kustomize
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests kustomize
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# Import Gatekeeper manifests
import-manifests: kustomize
	$(KUSTOMIZE) build github.com/open-policy-agent/gatekeeper/config/default/?ref=$(GATEKEEPER_VERSION) -o $(GATEKEEPER_MANIFEST_DIR)
	rm -f ./$(GATEKEEPER_MANIFEST_DIR)/~g_v1_namespace_gatekeeper-system.yaml

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	GOFLAGS=$(GOFLAGS) go vet ./...

# Generate code
generate: controller-gen
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

BINDATA_OUTPUT_FILE := ./pkg/bindata/bindata.go
.ensure-go-bindata:
	ln -s $(abspath ./vendor) "$${TMP_GOPATH}/src"
	export GO111MODULE=off && export GOPATH=$${TMP_GOPATH} && export GOBIN=$${TMP_GOPATH}/bin && GOFLAGS=$(GOFLAGS) go install "./vendor/github.com/go-bindata/go-bindata/..."
.PHONY: .ensure-go-bindata

.run-bindata: .ensure-go-bindata
	$${TMP_GOPATH}/bin/go-bindata -nocompress -nometadata \
		-prefix "bindata" \
		-pkg "bindata" \
		-o "$${BINDATA_OUTPUT_PREFIX}$(BINDATA_OUTPUT_FILE)" \
		-ignore "OWNERS" \
		./$(GATEKEEPER_MANIFEST_DIR)/... && \
	gofmt -s -w "$${BINDATA_OUTPUT_PREFIX}$(BINDATA_OUTPUT_FILE)"
.PHONY: .run-bindata

update-bindata:
	export TMP_GOPATH=$$(mktemp -d) ;\
	$(MAKE) .run-bindata ;\
	rm -rf "$${TMP_GOPATH}"
.PHONY: update-bindata

verify-bindata:
	export TMP_GOPATH=$$(mktemp -d) ;\
	export TMP_DIR=$$(mktemp -d) ;\
	export BINDATA_OUTPUT_PREFIX="$${TMP_DIR}/" ;\
	$(MAKE) .run-bindata ;\
	diff -Naup {.,$${TMP_DIR}}/$(BINDATA_OUTPUT_FILE) ;\
	rm -rf "$${TMP_DIR}" ;\
	rm -rf "$${TMP_GOPATH}"
.PHONY: verify-bindata

# Build the docker image
docker-build: test
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.4.0 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

kustomize:
ifeq (, $(shell which kustomize))
	@{ \
	set -e ;\
	KUSTOMIZE_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$KUSTOMIZE_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go get sigs.k8s.io/kustomize/kustomize/v3@v3.5.4 ;\
	rm -rf $$KUSTOMIZE_GEN_TMP_DIR ;\
	}
KUSTOMIZE=$(GOBIN)/kustomize
else
KUSTOMIZE=$(shell which kustomize)
endif

# Generate bundle manifests and metadata, then validate generated files.
.PHONY: bundle
bundle: manifests
	operator-sdk generate kustomize manifests -q
	cd config/manager && $(KUSTOMIZE) edit set image controller=$(IMG)
	$(KUSTOMIZE) build config/manifests | operator-sdk generate bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	operator-sdk bundle validate ./bundle

# Build the bundle image.
.PHONY: bundle-build
bundle-build:
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

.PHONY: vendor
vendor:
	GO111MODULE=on GOFLAGS=$(GOFLAGS) go mod vendor

.PHONY: tidy
tidy:
	GO111MODULE=on GOFLAGS=$(GOFLAGS) go mod tidy
