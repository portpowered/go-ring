GO ?= go
GO_TEST_TIMEOUT ?= 120s
export GOWORK := off
.DEFAULT_GOAL := check
.PHONY: check build build-examples test test-race test-cover test-integration fmt vet generate-api lint
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
	$(GO) test -tags integration ./tests/integration/... -timeout 5m
fmt:
	$(GO) fmt ./...
vet:
	$(GO) vet ./...

# The two CLIs own code generation; no repository-specific generator is used.
# oapi-codegen v2.8.0 uses a Go 1.25+ toolchain (GOTOOLCHAIN=auto).
generate-api:
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config pkg/generatedhttp/config.yaml api/openapi.yaml
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config pkg/ringapimodels/config.yaml api/client-models.openapi.yaml
	cd tools/protocols && npm ci && node -e "const fs=require('fs'); const dir='../../pkg/generatedsignaling'; for (const file of fs.readdirSync(dir)) if (file.endsWith('.go')) fs.unlinkSync(dir+'/'+file)" && npx --no-install modelina generate golang ../../api/asyncapi.yaml --packageName generatedsignaling --goIncludeTags -o ../../pkg/generatedsignaling
	$(GO) fmt ./pkg/generatedhttp ./pkg/generatedsignaling ./pkg/ringapimodels

lint:
	golangci-lint run ./...

# Optional maintainer checks; ordinary Go builds do not require Python or uv.
.PHONY: test-reference
test-reference:
	python tools/verify_reference.py
