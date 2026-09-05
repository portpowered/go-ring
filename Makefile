GO ?= go
GO_TEST_TIMEOUT ?= 120s
export GOWORK := off
.DEFAULT_GOAL := check
.PHONY: check build build-examples test test-race test-integration fmt vet
check: build test vet
build:
	$(GO) build ./...
build-examples:
	$(GO) build ./examples/...
test:
	$(GO) test ./... -timeout $(GO_TEST_TIMEOUT)
test-race:
	$(GO) test -race ./... -timeout $(GO_TEST_TIMEOUT)
test-integration:
	$(GO) test -tags integration ./test/integration/... -timeout 5m
fmt:
	$(GO) fmt ./...
vet:
	$(GO) vet ./...
