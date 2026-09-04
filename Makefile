.PHONY: build fix test

build:
	go build ./...

fix:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

test:
	go test ./...
