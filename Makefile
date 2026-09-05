LOCALBIN ?= $(CURDIR)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.20.0

.PHONY: build fix generate manifests test

build:
	go build ./...

fix:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object paths="./runtime/api/...;./operators/longhorizon/api/..."

manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) crd paths="./runtime/api/...;./operators/longhorizon/api/..." output:crd:artifacts:config=config/crd/bases
	cp config/crd/bases/*.yaml deploy/k8s/loopd/crds/

test:
	go test ./...

$(CONTROLLER_GEN):
	mkdir -p $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)
