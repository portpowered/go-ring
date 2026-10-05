package routegate_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/tools/routegate"
)

const signalingPrimitiveValueFinding = "unregistered-signaling-primitive-value"

type routegateFixtureError struct {
	operation string
	path      string
	cause     error
}

func (failure routegateFixtureError) Error() string {
	return failure.operation + " " + failure.path + ": " + failure.cause.Error()
}

func (failure routegateFixtureError) Unwrap() error {
	return failure.cause
}

//nolint:paralleltest // t.Chdir reproduces the routegate command's default root (GO-15).
func TestDefaultRoutegateCommandTracksSiblingNamedPrimitiveResults(t *testing.T) {
	root := signalingPrimitiveFixture(t, `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func startPlayback(req StartPlaybackRequest) any {
	entry := req.EntryPoint
	if entry == "" { entry = siblingFixedEntryPoint() }
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: entry})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`, `package ring
func siblingFixedEntryPoint() (result string) {
	result = "unregistered_library_entrypoint"
	return
}
`)
	assertRequestFixtureCompiles(t, root)
	copyRoutegateCommand(t, root)
	t.Chdir(root)

	command := exec.CommandContext(context.Background(), "go", "run", "./tools/routegate/cmd")

	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("default routegate command accepted a sibling named-result primitive:\n%s", output)
	}

	if !strings.Contains(string(output), signalingPrimitiveValueFinding) {
		t.Fatalf("default routegate command failed without the primitive provenance finding: %v\n%s", err, output)
	}
}

func TestCallerOpenSignalingPrimitiveSurvivesSiblingHelper(t *testing.T) {
	t.Parallel()

	root := signalingPrimitiveFixture(t, `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func StartPlayback(req StartPlaybackRequest) any {
	entry := siblingOpenEntryPoint(req.EntryPoint)
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: entry})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`, `package ring
func siblingOpenEntryPoint(value string) (result string) {
	result = value
	return
}
`)
	assertRequestFixtureCompiles(t, root)

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, finding := range findings {
		if finding.Rule == signalingPrimitiveValueFinding {
			t.Fatalf("caller-defined open playback value was rejected: %s", finding)
		}
	}
}

func TestSiblingCallbackKeepsPrimitiveLiteralProvenance(t *testing.T) {
	t.Parallel()

	root := signalingPrimitiveFixture(t, `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func startPlayback(req StartPlaybackRequest) any {
	entry := siblingCallbackEntryPoint(req.EntryPoint)
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: entry})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
func invokePlaybackCallback(callback func() string) (result string) {
	result = callback()
	return
}
`, `package ring
func siblingCallbackEntryPoint(input string) (result string) {
	callback := func() string {
		if input == "" { return "unregistered_callback_entrypoint" }
		return input
	}
	result = invokePlaybackCallback(callback)
	return
}
`)
	assertRequestFixtureCompiles(t, root)

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, finding := range findings {
		if finding.Rule == signalingPrimitiveValueFinding {
			return
		}
	}

	t.Fatalf("callback literal's schema-owned fallback escaped provenance checks: %v", findings)
}

func TestSiblingCallbackUsesDefiningFileImportAlias(t *testing.T) {
	t.Parallel()

	root := signalingPrimitiveFixture(t, `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func StartPlayback(req StartPlaybackRequest) any {
	entry := siblingCallbackEntryPoint(req.EntryPoint)
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: entry})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
func invokePlaybackCallback(callback func() string) (result string) {
	result = callback()
	return
}
`, `package ring
import wire "github.com/portpowered/go-ring/internal/protocol"
func siblingCallbackEntryPoint(input string) (result string) {
	callback := func() string {
		if input == "" { return wire.PlaybackEntryPointTimeline }
		return input
	}
	result = invokePlaybackCallback(callback)
	return
}
`)
	assertRequestFixtureCompiles(t, root)

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, finding := range findings {
		if finding.Rule == signalingPrimitiveValueFinding {
			t.Fatalf("callback's defining-file protocol import was not resolved: %s", finding)
		}
	}
}

func TestTraversalExhaustionCannotExemptDeepPrimitiveHelper(t *testing.T) {
	t.Parallel()

	const helperCount = 24

	var sibling strings.Builder

	sibling.WriteString(
		"package ring\n" +
			"func siblingDeepEntryPoint() (result string) { result = siblingDeepHelper00(); return }\n",
	)

	for index := range helperCount {
		value := fmt.Sprintf("siblingDeepHelper%02d", index)
		next := fmt.Sprintf("siblingDeepHelper%02d()", index+1)

		if index == helperCount-1 {
			next = `"unregistered_deep_helper_entrypoint"`
		}

		fmt.Fprintf(&sibling, "func %s() (result string) { result = %s; return }\n", value, next)
	}

	root := signalingPrimitiveFixture(t, `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func startPlayback(req StartPlaybackRequest) any {
	entry := siblingDeepEntryPoint()
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: entry})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`, sibling.String())
	assertRequestFixtureCompiles(t, root)

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, finding := range findings {
		if finding.Rule == signalingPrimitiveValueFinding {
			return
		}
	}

	t.Fatalf("deep library helper escaped provenance checks at traversal exhaustion: %v", findings)
}

func TestRecursionGuardCannotExemptFixedPrimitiveFallback(t *testing.T) {
	t.Parallel()

	root := signalingPrimitiveFixture(t, `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func startPlayback(req StartPlaybackRequest) any {
	entry := siblingRecursiveEntryPoint(req.EntryPoint, true)
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: entry})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`, `package ring
func siblingRecursiveEntryPoint(input string, again bool) (result string) {
	if again { return siblingRecursiveEntryPoint(input, false) }
	return "unregistered_recursive_entrypoint"
}
`)
	assertRequestFixtureCompiles(t, root)

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, finding := range findings {
		if finding.Rule == signalingPrimitiveValueFinding {
			return
		}
	}

	t.Fatalf("recursive helper's fixed fallback escaped provenance checks: %v", findings)
}

func TestSiblingNamedResultPreservesPackageConstantFieldProvenance(t *testing.T) {
	t.Parallel()

	caller := `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func startPlayback(req StartPlaybackRequest) any {
	entry := siblingNamedEntryPoint()
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: entry})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`
	sibling := `package ring
const siblingEntryPoint = "unregistered_sibling_entrypoint"
func siblingNamedEntryPoint() (result string) {
	local := siblingEntryPoint
	result = siblingValueAlias(local)
	return
}
func siblingValueAlias(value string) (result string) {
	result = value
	return
}
`
	root := signalingPrimitiveFixture(t, caller, sibling)
	assertRequestFixtureCompiles(t, root)

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	for _, finding := range findings {
		if finding.Rule == signalingPrimitiveValueFinding {
			return
		}
	}

	t.Fatalf("sibling package constant escaped schema-field provenance checks: %v", findings)
}

func TestPlaybackOfferHelperUsesActualCallsiteProvenance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		argument    string
		wantFinding bool
	}{
		{
			name:        "fixed library literal",
			argument:    `"unregistered_helper_entrypoint"`,
			wantFinding: true,
		},
		{
			name:        "caller request field",
			argument:    "req.EntryPoint",
			wantFinding: false,
		},
	}
	sibling := `package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
func siblingPlaybackOffer(input string) generatedsignaling.PlaybackOfferBody {
	return generatedsignaling.PlaybackOfferBody{EntryPoint: input}
}
`

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			caller := fmt.Sprintf(`package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func StartPlayback(req StartPlaybackRequest) any {
	return sendTyped(siblingPlaybackOffer(%s))
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`, test.argument)
			root := signalingPrimitiveFixture(t, caller, sibling)
			assertRequestFixtureCompiles(t, root)

			findings, err := routegate.Audit(root)
			if err != nil {
				t.Fatal(err)
			}

			found := false

			for _, finding := range findings {
				if finding.Rule == signalingPrimitiveValueFinding {
					found = true
				}
			}

			if found != test.wantFinding {
				t.Fatalf("helper callsite provenance finding = %t, want %t: %v", found, test.wantFinding, findings)
			}
		})
	}
}

func TestGeneratedPrimitiveValuesMatchTheirSchemaField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		constant    string
		wantFinding bool
	}{
		{name: "registered playback entry point", constant: "PlaybackEntryPointTimeline", wantFinding: false},
		{name: "unrelated signaling method", constant: "MethodPing", wantFinding: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			callerSource := fmt.Sprintf(`package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
import wire "github.com/portpowered/go-ring/internal/protocol"
type StartPlaybackRequest struct { EntryPoint string }
func startPlayback(req StartPlaybackRequest) any {
	return sendTyped(generatedsignaling.PlaybackOfferBody{EntryPoint: wire.%s})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`, test.constant)
			root := signalingPrimitiveFixture(t, callerSource, "package ring\n")
			assertRequestFixtureCompiles(t, root)

			findings, err := routegate.Audit(root)
			if err != nil {
				t.Fatal(err)
			}

			found := false

			for _, finding := range findings {
				if finding.Rule == signalingPrimitiveValueFinding {
					found = true
				}
			}

			if found != test.wantFinding {
				t.Fatalf("schema-field value finding = %t, want %t: %v", found, test.wantFinding, findings)
			}
		})
	}
}

func TestNumericSchemaConstantRequiresItsProtocolValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       string
		wantFinding bool
	}{
		{name: "protocol PTZ version", value: "protocol.PTZVersion", wantFinding: false},
		{name: "matching numeric literal", value: "1", wantFinding: true},
		{name: "wrong numeric literal", value: "2", wantFinding: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			callerSource := fmt.Sprintf(`package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
import protocol "github.com/portpowered/go-ring/internal/protocol"
func startPlayback() any {
	_ = protocol.PTZVersion
	return generatedsignaling.PtzWireParams{Version: %s}
}
`, test.value)
			root := signalingPrimitiveFixture(t, callerSource, "package ring\n")
			writeFixtureFile(t, root, "api/asyncapi.yaml", `asyncapi: 2.6.0
info: {title: numeric primitive fixture, version: 0.1.0}
servers: {}
channels: {}
operations: {}
components:
  messages: {}
  schemas:
    PTZWireParams:
      type: object
      required: [version]
      properties:
        version: {type: integer, const: 1}
    PlaybackOfferBody:
      type: object
      required: [entry_point]
      properties:
        entry_point: {type: string, x-extensible-enum: [timeline]}
`)

			model := `// Code generated by fixture. DO NOT EDIT.
package signaling
type PtzWireParams struct { Version int ` + "`json:\"version\"`" + ` }
`
			writeFixtureFile(t, root, "pkg/dependencymodels/signaling/ptz_wire_params.go", model)
			writeFixtureFile(t, root, "internal/protocol/values.go", `package protocol
const (
	PlaybackEntryPointTimeline = "timeline"
	MethodPing = "ping"
	PTZVersion = 1
)
`)
			assertRequestFixtureCompiles(t, root)

			findings, err := routegate.Audit(root)
			if err != nil {
				t.Fatal(err)
			}

			found := false

			for _, finding := range findings {
				if finding.Rule == signalingPrimitiveValueFinding {
					found = true
				}
			}

			if found != test.wantFinding {
				t.Fatalf("numeric schema-constant provenance finding = %t, want %t: %v", found, test.wantFinding, findings)
			}
		})
	}
}

func TestOptionalEmptyPrimitiveHonorsPointerJSONEncoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		modelField  string
		value       string
		wantFinding bool
	}{
		{
			name:        "omitted zero string",
			modelField:  `EntryPoint string ` + "`json:\"entry_point,omitempty\"`",
			value:       `EntryPoint: entry`,
			wantFinding: false,
		},
		{
			name:        "serialized nonnil empty string pointer",
			modelField:  `EntryPoint *string ` + "`json:\"entry_point,omitempty\"`",
			value:       `EntryPoint: &entry`,
			wantFinding: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			callerSource := fmt.Sprintf(`package ring
import generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
type StartPlaybackRequest struct { EntryPoint string }
func startPlayback(req StartPlaybackRequest) any {
	entry := ""
	return sendTyped(generatedsignaling.PlaybackOfferBody{%s})
}
func sendTyped(body generatedsignaling.PlaybackOfferBody) any { return body }
`, test.value)
			root := signalingPrimitiveFixture(t, callerSource, "package ring\n")
			model := `// Code generated by fixture. DO NOT EDIT.
package signaling
type PlaybackOfferBody struct { ` + test.modelField + ` }
`
			writeFixtureFile(t, root, "pkg/dependencymodels/signaling/playback_offer_body.go", model)
			assertRequestFixtureCompiles(t, root)

			findings, err := routegate.Audit(root)
			if err != nil {
				t.Fatal(err)
			}

			found := false

			for _, finding := range findings {
				if finding.Rule == signalingPrimitiveValueFinding {
					found = true
				}
			}

			if found != test.wantFinding {
				t.Fatalf("pointer-aware empty-value finding = %t, want %t: %v", found, test.wantFinding, findings)
			}
		})
	}
}

func signalingPrimitiveFixture(t *testing.T, callerSource, siblingSource string) string {
	t.Helper()

	root := fixtureRoot(t, "package sample\n")
	writeFixtureFile(t, root, "go.mod", `module github.com/portpowered/go-ring

go 1.24.0

require gopkg.in/yaml.v3 v3.0.1
`)
	writeFixtureFile(t, root, "api/asyncapi.yaml", `asyncapi: 2.6.0
info: {title: primitive fixture, version: 0.1.0}
servers: {}
channels: {}
operations: {}
components:
  messages: {}
  schemas:
    PlaybackOfferBody:
      type: object
      required: [entry_point]
      properties:
        entry_point: {type: string, x-extensible-enum: [timeline]}
`)
	writeFixtureFile(
		t,
		root,
		"pkg/dependencymodels/signaling/playback_offer_body.go",
		`// Code generated by fixture. DO NOT EDIT.
package signaling
type PlaybackOfferBody struct { EntryPoint string `+"`json:\"entry_point\"`"+` }
	`,
	)
	writeFixtureFile(t, root, "pkg/ring/signaling_extras.go", callerSource)
	writeFixtureFile(t, root, "pkg/ring/sibling.go", siblingSource)
	writeFixtureFile(t, root, "internal/protocol/values.go", `package protocol
const (
	PlaybackEntryPointTimeline = "timeline"
	MethodPing = "ping"
)
`)

	return root
}

func copyRoutegateCommand(t *testing.T, destination string) {
	t.Helper()

	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	repositoryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	sourceRoot := filepath.Join(repositoryRoot, "tools", "routegate")

	err = filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return routegateFixtureError{operation: "walk routegate source", path: path, cause: walkErr}
		}

		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		relative, relativeErr := filepath.Rel(repositoryRoot, path)
		if relativeErr != nil {
			return routegateFixtureError{operation: "relativize routegate source", path: path, cause: relativeErr}
		}

		target := filepath.Join(destination, relative)

		mkdirErr := os.MkdirAll(filepath.Dir(target), 0o700)
		if mkdirErr != nil {
			return routegateFixtureError{
				operation: "create routegate fixture directory",
				path:      filepath.Dir(target),
				cause:     mkdirErr,
			}
		}

		contents, readErr := os.ReadFile(path) // #nosec G304 -- source paths are under the checked-in routegate package.
		if readErr != nil {
			return routegateFixtureError{operation: "read routegate source", path: path, cause: readErr}
		}

		writeErr := os.WriteFile(target, contents, 0o600)
		if writeErr != nil {
			return routegateFixtureError{operation: "write routegate fixture source", path: target, cause: writeErr}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	goSumPath := filepath.Join(repositoryRoot, "go.sum")

	goSum, err := os.ReadFile(goSumPath) // #nosec G304 -- repositoryRoot identifies the checked-in repository.
	if err != nil {
		t.Fatal(err)
	}

	writeErr := os.WriteFile(filepath.Join(destination, "go.sum"), goSum, 0o600)
	if writeErr != nil {
		t.Fatal(routegateFixtureError{operation: "write routegate fixture", path: "go.sum", cause: writeErr})
	}
}
