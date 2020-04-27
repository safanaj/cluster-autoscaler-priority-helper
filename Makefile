VERSION = 0.2
REGISTRY ?= registry2.swarm.devfactory.com/central
FLAGS =
ENVVAR = CGO_ENABLED=0
GOOS ?= linux
LDFLAGS ?=
COMPONENT = cluster-autoscaler-priority-helper

DOCKER_IMAGE = "${REGISTRY}/${COMPONENT}:${VERSION}"

K8S_VERSION = 1.14.8
CLIGO_VERSION = v11.0.1-0.20191029005444-8e4128053008+incompatible
OPENAPI_VERSION = v0.0.0-20190816220812-743ec37842bf
GNOSTIC_VERSION = v0.0.0-20170729233727-0c5108395e2d

.PHONY: build static install_deps deps clean

golang:
	@echo "--> Go Version"
	@go version

install_deps:
	go get -d \
		k8s.io/kubernetes@v${K8S_VERSION} \
		k8s.io/api@kubernetes-${K8S_VERSION} \
		k8s.io/apimachinery@kubernetes-${K8S_VERSION} \
		k8s.io/component-base@kubernetes-${K8S_VERSION} \
		k8s.io/client-go@${CLIGO_VERSION} \
		k8s.io/kube-openapi@${OPENAPI_VERSION} \
		github.com/googleapis/gnostic@${GNOSTIC_VERSION}

deps:
	go mod verify && go mod tidy -v && go mod vendor -v

clean:
	rm -f ${COMPONENT}

clean-all: clean
	rm -rf vendor

build: golang
	@echo "--> Compiling the project"
	#$(ENVVAR) GOOS=$(GOOS) go install $(LDFLAGS) -v ./pkg/...
	$(ENVVAR) GOOS=$(GOOS) go build -mod=vendor $(LDFLAGS) \
				-ldflags "-w -X main.version=${VERSION}" -v -o ${COMPONENT} ./cmd/...

static: golang
	@echo "--> Compiling the static binary"
	#$(ENVVAR) GOOS=$(GOOS) go install $(LDFLAGS) -v ./pkg/...
	$(ENVVAR) GOARCH=amd64 GOOS=$(GOOS) \
		go build -mod=vendor -a -tags netgo \
			-ldflags "-w -X main.version=${VERSION}" -v -o ${COMPONENT} ./cmd/...

test:
	$(ENVVAR) GOOS=$(GOOS) go test -v ./...

docker: install_deps deps static
	docker build -t ${DOCKER_IMAGE} .
