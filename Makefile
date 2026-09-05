LOCALBIN ?= $(CURDIR)/bin
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
CONTROLLER_TOOLS_VERSION ?= v0.20.0

.PHONY: build fix generate manifests test

build:
	go build ./...

fix:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object paths="./runtime/api/..."

manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) crd paths="./runtime/api/..." output:crd:artifacts:config=config/crd/bases
	cp config/crd/bases/loopd.compforge.io_conversations.yaml deploy/k8s/loopd/crds/loopd.compforge.io_conversations.yaml

test:
	go test ./...

$(CONTROLLER_GEN):
	mkdir -p $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)
