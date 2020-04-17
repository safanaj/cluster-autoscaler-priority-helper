VERSION = 0.1
REGISTRY ?= staging-k8s.gcr.io
FLAGS =
ENVVAR = CGO_ENABLED=0
GOOS ?= linux
LDFLAGS ?=
COMPONENT = cluster-autoscaler-priority-helper

DOCKER_IMAGE = "registry2.swarm.devfactory.com/central/${COMPONENT}:${VERSION}"

K8S_VERSION = 1.14.8

.PHONY: build static install_deps deps clean

golang:
	@echo "--> Go Version"
	@go version

install_deps:
	go get -d \
		k8s.io/api@kubernetes-${K8S_VERSION} \
		k8s.io/apimachinery@kubernetes-${K8S_VERSION} \
		k8s.io/client-go@v11.0.1-0.20191029005444-8e4128053008+incompatible

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
