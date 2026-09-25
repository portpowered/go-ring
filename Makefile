GO ?= go
GO_TEST_TIMEOUT ?= 120s
export GOWORK := off
.DEFAULT_GOAL := check
.PHONY: check build build-examples test test-race test-cover test-integration fmt vet
check: build test vet
build:
	$(GO) build ./...
build-examples:
	$(GO) build ./examples/...
test:
	$(GO) test ./... -timeout $(GO_TEST_TIMEOUT)
test-race:
	$(GO) test -race ./... -timeout $(GO_TEST_TIMEOUT)
test-cover:
	$(GO) run ./tools/coverage
test-integration:
	$(GO) test -tags integration ./test/integration/... -timeout 5m
fmt:
	$(GO) fmt ./...
vet:
	$(GO) vet ./...

# Optional maintainer checks; ordinary Go builds do not require Python or uv.
.PHONY: test-reference
test-reference:
	python tools/verify_reference.py
