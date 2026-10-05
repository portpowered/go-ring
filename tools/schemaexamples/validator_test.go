package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidNestedAndContentSchemaExamplesCoverOpenAPIGroups(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "openapi.yaml", `openapi: 3.1.0
info: {title: fixture, version: "1"}
paths:
  /thing:
    post:
      requestBody:
        content:
          application/json:
            schema: {$ref: ./schemas.yaml#/components/schemas/Envelope}
      responses:
        "200":
          description: success
          content:
            application/json:
              schema: {$ref: ./schemas.yaml#/components/schemas/Envelope}
        "422":
          description: failure
          content:
            application/json:
              schema: {$ref: ./schemas.yaml#/components/schemas/Envelope}
`)
	writeFixture(t, root, "schemas.yaml", `components:
  schemas:
    Envelope:
      type: object
      required: [state, requestData]
      properties:
        state:
          type: string
          enum: [ready, failed]
          example: ready
          x-example-evidence: synthetic
        requestData:
          type: string
          contentMediaType: application/json
          contentSchema:
            type: object
            required: [state]
            properties:
              state: {type: string, enum: [ready, failed]}
          example: '{"state":"ready"}'
          x-example-evidence: synthetic
      example: {state: ready, requestData: '{"state":"ready"}'}
      x-example-evidence: synthetic
`)

	got, err := validateRoots([]string{root})
	if err != nil {
		t.Fatalf("validate fixture: %v", err)
	}

	if got.examples != 3 || got.groups != 3 {
		t.Fatalf("report = %+v, want 3 schema examples and 3 sample groups", got)
	}
}

func TestInvalidNestedEnumSampleFailsWithoutPrintingValue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "api.yaml", `openapi: 3.1.0
info: {title: fixture, version: "1"}
paths: {}
components:
  schemas:
    Envelope:
      type: object
      properties:
        state:
          type: string
          enum: [ready, failed]
          example: PRIVATE_INVALID_STATE
          x-example-evidence: synthetic
`)

	_, err := validateRoots([]string{root})
	if err == nil {
		t.Fatal("invalid nested enum example unexpectedly passed")
	}

	message := err.Error()
	if !strings.Contains(message, "does not satisfy") {
		t.Fatalf("diagnostic does not identify the schema failure: %v", err)
	}

	if strings.Contains(message, "PRIVATE_INVALID_STATE") {
		t.Fatalf("diagnostic leaked sample value: %v", err)
	}
}

func TestContentSchemaRejectsInvalidJSONEncodedSample(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "schema.yaml", `type: string
contentMediaType: application/json
contentSchema:
  type: object
  required: [state]
  properties:
    state: {type: string, enum: [ready, failed]}
example: '{"state":"PRIVATE_INVALID_STATE"}'
x-example-evidence: synthetic
`)

	_, err := validateRoots([]string{root})
	if err == nil {
		t.Fatal("invalid JSON encoded contentSchema sample unexpectedly passed")
	}

	if strings.Contains(err.Error(), "PRIVATE_INVALID_STATE") {
		t.Fatalf("diagnostic leaked sample value: %v", err)
	}
}

func TestMissingRequiredGroupsAndUnresolvedOwnerFail(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "api.yaml", `openapi: 3.1.0
info: {title: fixture, version: "1"}
paths:
  /thing:
    get:
      responses:
        "200":
          description: success
          content:
            application/json:
              schema: {$ref: ./missing.yaml#/components/schemas/Thing}
`)

	_, err := validateRoots([]string{root})
	if err == nil {
		t.Fatal("unresolved schema owner and missing sample group unexpectedly passed")
	}

	message := err.Error()
	if !strings.Contains(message, "unresolved reference") || !strings.Contains(message, "missing required sample group") {
		t.Fatalf("diagnostic omitted reference or group failure: %v", err)
	}
}

func TestGroupedComponentExampleResolvesFromCanonicalOwner(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeGroupedFixture(t, root, "Shared")
	writeFixture(t, root, "api/schemas/common.yaml", `components:
  schemas:
    Shared:
      type: string
      enum: [ready]
      example: ready
      x-example-evidence: synthetic
`)
	writeFixture(t, root, "api/openapi.yaml", `openapi: 3.1.0
info: {title: stale generated bundle, version: "1"}
components:
  schemas:
    Shared: {type: integer, example: 99, x-example-evidence: synthetic}
`)

	got, err := validateRoots([]string{filepath.Join(root, "api")})
	if err != nil {
		t.Fatalf("validate grouped canonical schema: %v", err)
	}

	if got.examples != 1 || got.groups != 1 {
		t.Fatalf("report = %+v, want canonical component example and one group", got)
	}
}

func TestStaleGeneratedBundleCannotHideUnresolvedCanonicalReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeGroupedFixture(t, root, "OnlyInGeneratedBundle")
	writeFixture(t, root, "api/schemas/common.yaml", `components:
  schemas:
    Shared: {type: string, example: ready, x-example-evidence: synthetic}
`)
	writeFixture(t, root, "api/openapi.yaml", `openapi: 3.1.0
info: {title: stale generated bundle, version: "1"}
components:
  schemas:
    OnlyInGeneratedBundle: {type: string, example: ready, x-example-evidence: synthetic}
`)

	_, err := validateRoots([]string{filepath.Join(root, "api")})
	if err == nil || !strings.Contains(err.Error(), "unresolved reference") {
		t.Fatalf("stale generated component hid unresolved canonical reference: %v", err)
	}
}

func writeGroupedFixture(t *testing.T, root, referencedSchema string) {
	t.Helper()
	writeFixture(t, root, "api/model-groups.json", `{
  "input": "api/openapi.base.yaml",
  "groups": [{"name": "common", "source": "api/schemas/common.yaml"}]
}`)
	writeFixture(t, root, "api/openapi.base.yaml", `openapi: 3.1.0
info: {title: grouped source, version: "1"}
paths:
  /grouped:
    get:
      responses:
        "200":
          description: success
          content:
            application/json:
              schema: {$ref: "#/components/schemas/`+referencedSchema+`"}
`)
}

func TestAsyncAPIMessageExampleIsValidated(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "asyncapi.yaml", `asyncapi: 3.0.0
info: {title: fixture, version: "1"}
channels:
  events:
    messages:
      update:
        payload:
          type: object
          required: [state]
          properties:
            state: {type: string, enum: [ready, failed]}
        examples:
          - payload: {state: ready}
        x-example-evidence: synthetic
`)

	got, err := validateRoots([]string{root})
	if err != nil {
		t.Fatalf("validate AsyncAPI fixture: %v", err)
	}

	if got.examples != 1 || got.groups != 1 {
		t.Fatalf("report = %+v, want one message example and group", got)
	}
}

func TestAsyncAPIInvalidExampleFails(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "asyncapi.yaml", `asyncapi: 3.0.0
info: {title: fixture, version: "1"}
channels:
  events:
    messages:
      update:
        payload:
          type: object
          required: [state]
          properties:
            state: {type: string, enum: [ready, failed]}
        examples:
          - payload: {state: PRIVATE_INVALID_STATE}
        x-example-evidence: synthetic
`)

	_, err := validateRoots([]string{root})
	if err == nil {
		t.Fatal("invalid AsyncAPI example unexpectedly passed")
	}

	if strings.Contains(err.Error(), "PRIVATE_INVALID_STATE") {
		t.Fatalf("diagnostic leaked sample value: %v", err)
	}
}

func TestAsyncAPIExampleObjectEvidenceDoesNotSatisfyMessageSample(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "asyncapi.yaml", `asyncapi: 3.0.0
info: {title: fixture, version: "1"}
channels:
  events:
    messages:
      update:
        payload:
          type: object
          required: [state]
          properties:
            state: {type: string, enum: [ready, failed]}
        examples:
          - payload: {state: ready}
            x-example-evidence: synthetic
`)

	_, err := validateRoots([]string{root})
	if err == nil || !strings.Contains(err.Error(), "sample requires x-example-evidence: synthetic") {
		t.Fatalf("Example Object evidence satisfied AsyncAPI message sample: %v", err)
	}
}

func TestAsyncAPIExampleObjectRejectsEvidenceWithMessageEvidence(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFixture(t, root, "asyncapi.yaml", `asyncapi: 3.0.0
info: {title: fixture, version: "1"}
channels:
  events:
    messages:
      update:
        payload:
          type: object
          required: [state]
          properties:
            state: {type: string, enum: [ready, failed]}
        examples:
          - payload: {state: ready}
            x-example-evidence: synthetic
        x-example-evidence: synthetic
`)

	_, err := validateRoots([]string{root})
	if err == nil || !strings.Contains(err.Error(), "AsyncAPI Example Object evidence belongs on the Message Object") {
		t.Fatalf("AsyncAPI Example Object evidence was not rejected: %v", err)
	}
}

func writeFixture(t *testing.T, root, name, contents string) {
	t.Helper()

	path := filepath.Join(root, name)

	err := os.MkdirAll(filepath.Dir(path), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(contents), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
