GO ?= go
GOLANGCI_LINT_VERSION := v2.3.0
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GO_TEST_TIMEOUT ?= 120s
export GOWORK := off
.DEFAULT_GOAL := check
.PHONY: check check-cli build build-examples build-cli test test-cli test-race test-stress test-cover test-integration test-contracts fmt vet vet-cli generate-api routegate lint lint-cli
check: build build-cli test-contracts test test-cli vet vet-cli routegate
check-cli: build-cli test-cli vet-cli
build:
	$(GO) build ./...
build-examples:
	$(GO) build ./examples/...
build-cli:
	cd cmd/go-ring && $(GO) build ./...
test:
	$(GO) test ./... -timeout $(GO_TEST_TIMEOUT)
test-cli:
	cd cmd/go-ring && $(GO) test -race ./...
test-contracts:
	npm ci --prefix tools/protocols --ignore-scripts
	$(GO) test ./tools/protocols ./tools/capture
test-race:
	$(GO) test -race ./... -timeout $(GO_TEST_TIMEOUT)
test-stress:
	$(GO) test -race ./internal/signaling ./pkg/ring -run 'Test(SessionAdversarialTerminationStress|DeviceSessionAdversarialCloseStress|SignalingConnectionAdversarialTerminationStress|CloseWaitsForInFlightPTZAndSendsSafetyStop)' -count=20 -timeout $(GO_TEST_TIMEOUT)
test-cover:
	$(GO) run ./tools/coverage
	$(GO) run ./tools/coverage -suite unit
	$(GO) run ./tools/coverage -suite combined
.PHONY: test-cover-integration
test-cover-integration:
	$(GO) run ./tools/coverage -suite integration
test-integration:
	$(GO) test -tags integration ./tests/integration/... -timeout 5m
fmt:
	$(GO) fmt ./...
	cd cmd/go-ring && $(GO) fmt ./...
vet:
	$(GO) vet ./...
vet-cli:
	cd cmd/go-ring && $(GO) vet ./...
routegate:
	$(GO) run ./tools/routegate/cmd

# OpenAPI uses oapi-codegen; AsyncAPI uses Modelina's published Go generator API.
# oapi-codegen v2.8.0 uses a Go 1.25+ toolchain (GOTOOLCHAIN=auto).
generate-api:
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/generatedhttp/config.yaml api/openapi.yaml
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config pkg/generatedhttp/config.yaml api/openapi.yaml
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/generatedfcm/config.yaml api/external/fcm.openapi.yaml
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config pkg/ringapimodels/config.yaml api/client-models.openapi.yaml
	cd tools/protocols && npm ci && node generate_signaling.mjs && node generate_protocol_constants.mjs
	cd tools/protocols && node generate_mcs.mjs
	$(GO) fmt ./internal/generatedhttp ./internal/generatedfcm ./internal/generatedsignaling ./internal/protocol ./pkg/generatedhttp ./pkg/generatedsignaling ./pkg/ringapimodels

lint:
	$(GOLANGCI_LINT) run ./...
	$(GO) test ./tools/lint
	$(GO) run ./tools/routegate/cmd
	cd cmd/go-ring && $(GOLANGCI_LINT) run ./...
lint-cli:
	cd cmd/go-ring && $(GOLANGCI_LINT) run ./...
