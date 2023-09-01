
# Image URL to use all building/pushing image targets
IMG ?= controller:latest
# Produce CRDs that work back to Kubernetes 1.11 (no version conversion)
CRD_OPTIONS ?= "crd:crdVersions=v1"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
TOOLS_DIR := hack/tools
BIN_DIR := bin
TOOLS_BIN_DIR := $(TOOLS_DIR)/$(BIN_DIR)
ABS_TOOLS_BIN_DIR := $(abspath $(TOOLS_BIN_DIR))
CONTROLLER_GEN := $(ABS_TOOLS_BIN_DIR)/controller-gen
CONVERSION_GEN := $(ABS_TOOLS_BIN_DIR)/conversion-gen

export PATH := $(abspath $(TOOLS_BIN_DIR)):$(PATH)

# Set --output-base for conversion-gen if we are not within GOPATH
ifneq ($(abspath $(ROOT_DIR)),$(shell go env GOPATH)/src/github.com/aws/etcdadm-controller)
	CONVERSION_GEN_OUTPUT_BASE := --output-base=$(ROOT_DIR)
else
	export GOPATH := $(shell go env GOPATH)
endif

all: manager

# Run tests
test: generate fmt vet manifests
	go test ./... -coverprofile cover.out

# Build manager binary
manager: fmt vet
	CGO_ENABLED=0 go build -ldflags='-s -w -extldflags="-static" -buildid=""' -trimpath -o bin/manager main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go

# Install CRDs into a cluster
install: manifests
	kustomize build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests
	kustomize build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests
	cd config/manager && kustomize edit set image controller=${IMG}
	kustomize build config/default | kubectl apply -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager-role webhook \
		paths="./..." \
		output:rbac:dir=./config/rbac \
		output:crd:artifacts:config=config/crd/bases

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

generate-conversion: $(CONVERSION_GEN)
	$(CONVERSION_GEN) \
		--input-dirs=./api/v1alpha3 \
		--build-tag=ignore_autogenerated_etcd_cluster \
		--extra-peer-dirs=github.com/aws/etcdadm-bootstrap-provider/api/v1alpha3 \
		--output-file-base=zz_generated.conversion $(CONVERSION_GEN_OUTPUT_BASE) \
		--go-header-file=hack/boilerplate.go.txt \
		--alsologtostderr

build: docker-build

# Build the docker image
docker-build: manager
	docker build . -t ${IMG}

# Push the docker image
docker-push:
	docker push ${IMG}

.PHONY: lint
lint: bin/golangci-lint ## Run golangci-lint
	bin/golangci-lint run

bin/golangci-lint: ## Download golangci-lint
bin/golangci-lint: GOLANGCI_LINT_VERSION?=$(shell cat .github/workflows/golangci-lint.yml | yq e '.jobs.golangci.steps[] | select(.name == "golangci-lint") .with.version' -)
bin/golangci-lint:
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s $(GOLANGCI_LINT_VERSION)

.PHONY: clean
clean:
	rm -Rf ./bin

.PHONY: mocks
mocks: export GOPATH := $(shell go env GOPATH)
mocks: MOCKGEN := ${GOPATH}/bin/mockgen --build_flags=--mod=mod
mocks: ## Generate mocks
	go install github.com/golang/mock/mockgen@v1.6.0
	${MOCKGEN} -destination controllers/mocks/roundtripper.go -package=mocks net/http RoundTripper
	${MOCKGEN} -destination controllers/mocks/etcdclient.go -package=mocks -source "controllers/controller.go" EtcdClient

.PHONY: verify-mocks
verify-mocks: mocks ## Verify if mocks need to be updated
	$(eval DIFF=$(shell git diff --raw -- '*.go' | wc -c))
	if [[ $(DIFF) != 0 ]]; then \
		echo "Detected out of date mocks"; \
		exit 1;\
	fi

$(CONTROLLER_GEN): $(TOOLS_BIN_DIR) # Build controller-gen from tools folder.
	GOBIN=$(ABS_TOOLS_BIN_DIR) go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.11.4

$(CONVERSION_GEN): $(TOOLS_BIN_DIR)
	GOBIN=$(ABS_TOOLS_BIN_DIR) go install k8s.io/code-generator/cmd/conversion-gen@v0.26.0

$(TOOLS_BIN_DIR):
	mkdir -p $(TOOLS_BIN_DIR)
