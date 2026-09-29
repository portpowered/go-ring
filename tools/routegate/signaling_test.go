package routegate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/tools/routegate"
)

const signalingWireFixture = `package websocket

import (
	"encoding/json"
	"fmt"
	protocol "github.com/portpowered/go-ring/internal/protocol"
	signaling "github.com/portpowered/go-ring/internal/signaling"
	generatedsignaling "github.com/portpowered/go-ring/pkg/generatedsignaling"
)

func marshalSignalingFrame(message signaling.Message) ([]byte, error) {
	switch message.Method {
	case protocol.MethodPing:
		return marshalGeneratedFrame(
			message,
			protocol.MethodPing,
			[]string{"nonce"},
			func(body *generatedsignaling.PingBody) any {
				return generatedsignaling.PingFrame{Method: protocol.MethodPing, Body: body}
			},
		)
	default:
		return nil, fmt.Errorf("unsupported method %q", message.Method)
	}
}

func marshalGeneratedFrame[Body any](
	message signaling.Message,
	method string,
	required []string,
	build func(*Body) any,
) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(message.Body, &fields); err != nil {
		return nil, signalingWireError("decode body")
	}
	var body Body
	if err := json.Unmarshal(message.Body, &body); err != nil {
		return nil, signalingWireError("decode generated body")
	}
	return json.Marshal(build(&body))
}

func unmarshalSignalingFrame(encoded []byte) (signaling.Message, error) {
	var discriminator generatedsignaling.SignalingInboundDiscriminator
	if err := json.Unmarshal(encoded, &discriminator); err != nil {
		return signaling.Message{}, err
	}
	if discriminator.Method.Value() == nil {
		return signaling.Message{}, fmt.Errorf("unknown method")
	}
	if err := validateInboundFrame(encoded, signalingEnvelope{}); err != nil {
		return signaling.Message{}, err
	}
	return signaling.Message{}, nil
}

type signalingEnvelope struct{ method string }

func validateInboundFrame(encoded []byte, envelope signalingEnvelope) error {
	switch envelope.method {
	case protocol.MethodPong:
		return validateTypedInbound[generatedsignaling.PongFrame](encoded)
	default:
		return fmt.Errorf("unsupported method %q", envelope.method)
	}
}

func validateTypedInbound[Frame any](encoded []byte) error { return nil }

func signalingWireError(format string, values ...any) error { return fmt.Errorf(format, values...) }
`

const signalingWriterFixture = `package websocket

import (
	"github.com/gorilla/websocket"
	signaling "github.com/portpowered/go-ring/internal/signaling"
)

func WriteSignaling(conn *websocket.Conn, message signaling.Message) error {
	encoded, err := marshalSignalingFrame(message)
	if err != nil { return err }
	return conn.WriteMessage(websocket.TextMessage, encoded)
}
`

func TestSignalingAdapterChecksAcceptSchemaBoundFrames(t *testing.T) {
	t.Parallel()

	findings := auditSignalingFixture(t, signalingWireFixture, signalingWriterFixture)
	if len(findings) != 0 {
		t.Fatalf("valid generated signaling adapter produced findings: %+v", findings)
	}
}

func TestGeneratedFrameDiscoveryPrefersInternalPackage(t *testing.T) {
	t.Parallel()

	root := fixtureRoot(t, "package sample\n")
	writeFixtureFile(t, root, "api/asyncapi.yaml", signalingAsyncAPI)

	models := `package generatedsignaling
type PingFrame struct{}
type PingBody struct{}
type PongFrame struct{}
type PongBody struct{}
`
	writeFixtureFile(t, root, "internal/generatedsignaling/models.go", models)
	writeFixtureFile(t, root, "pkg/generatedsignaling/models.go", models)

	contracts, err := routegate.LoadContracts(root)
	if err != nil {
		t.Fatal(err)
	}

	want := "example.com/routegatefixture/internal/generatedsignaling"
	if contracts.GeneratedFrames["PingFrame"] != want || contracts.GeneratedFrames["PingBody"] != want {
		t.Fatalf(
			"generated signaling package = (%q, %q), want %q",
			contracts.GeneratedFrames["PingFrame"],
			contracts.GeneratedFrames["PingBody"],
			want,
		)
	}

	if len(contracts.Diagnostics) != 0 {
		t.Fatalf("canonical generated package produced diagnostics: %+v", contracts.Diagnostics)
	}
}

func TestSignalingAdapterRejectsMissingOrMismatchedOutboundFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			name:   "missing method branch",
			before: "case protocol.MethodPing:",
			after:  "case protocol.MethodPong:",
		},
		{
			name:   "wrong generated frame",
			before: "return generatedsignaling.PingFrame{Method: protocol.MethodPing, Body: body}",
			after:  "return generatedsignaling.PongFrame{Method: protocol.MethodPing, Body: body}",
		},
		{
			name:   "wrong generated method",
			before: "PingFrame{Method: protocol.MethodPing, Body: body}",
			after:  "PingFrame{Method: protocol.MethodPong, Body: body}",
		},
		{
			name:   "wrong helper method",
			before: "return marshalGeneratedFrame(\n\t\t\tmessage,\n\t\t\tprotocol.MethodPing,",
			after:  "return marshalGeneratedFrame(\n\t\t\tmessage,\n\t\t\tprotocol.MethodPong,",
		},
		{
			name:   "wrong generated body",
			before: "func(body *generatedsignaling.PingBody)",
			after:  "func(body *generatedsignaling.PongBody)",
		},
		{
			name:   "nonrejecting default",
			before: "return nil, fmt.Errorf(\"unsupported method %q\", message.Method)",
			after:  "return nil, nil",
		},
		{
			name:   "raw message marshal",
			before: "return json.Marshal(build(&body))",
			after:  "return json.Marshal(message)",
		},
		{
			name:   "nonexhaustive outer dispatch",
			before: "switch message.Method {",
			after:  "if message.Method == \"ping\" { return json.Marshal(message) }; switch message.Method {",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := replaceOnce(t, signalingWireFixture, test.before, test.after)

			findings := auditSignalingFixture(t, source, signalingWriterFixture)
			if len(findings) == 0 {
				t.Fatal("mutated outbound adapter passed the schema audit")
			}
		})
	}
}

func TestSignalingAdapterRejectsMissingOrMismatchedInboundFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		before string
		after  string
	}{
		{
			name:   "wrong frame for method",
			before: "return validateTypedInbound[generatedsignaling.PongFrame](encoded)",
			after:  "return validateTypedInbound[generatedsignaling.PingFrame](encoded)",
		},
		{
			name:   "nonrejecting default",
			before: "return fmt.Errorf(\"unsupported method %q\", envelope.method)",
			after:  "return nil",
		},
		{
			name:   "skip generated discriminator",
			before: "var discriminator generatedsignaling.SignalingInboundDiscriminator",
			after:  "var discriminator generatedsignaling.PingFrame",
		},
		{
			name:   "nonexhaustive outer dispatch",
			before: "switch envelope.method {",
			after:  "if envelope.method == \"pong\" { return nil }; switch envelope.method {",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := replaceOnce(t, signalingWireFixture, test.before, test.after)

			findings := auditSignalingFixture(t, source, signalingWriterFixture)
			if len(findings) == 0 {
				t.Fatal("mutated inbound adapter passed the schema audit")
			}
		})
	}
}

func auditSignalingFixture(t *testing.T, wire, writer string) []string {
	t.Helper()

	root := fixtureRoot(t, "package sample\n")
	write := func(name, contents string) { writeFixtureFile(t, root, name, contents) }

	write("go.mod", "module github.com/portpowered/go-ring\n\ngo 1.24.0\n")
	write("pkg/dependencies/websocket/wire.go", wire)
	write("pkg/dependencies/websocket/signaling.go", writer)
	write("internal/protocol/endpoints.go", "package protocol\n")
	write("internal/protocol/signaling.go", `package protocol
const (
	MethodPing = "ping"
	MethodPong = "pong"
)
`)
	write("pkg/generatedsignaling/models.go", `package generatedsignaling
type PingFrame struct{}
type PingBody struct{}
type PongFrame struct{}
type PongBody struct{}
type SignalingInboundDiscriminator struct{ Method methodValue }
type methodValue struct{}
func (methodValue) Value() *string { return nil }
`)
	write("api/asyncapi.yaml", signalingAsyncAPI)

	findings, err := routegate.Audit(root)
	if err != nil {
		t.Fatal(err)
	}

	result := make([]string, 0, len(findings))
	for _, finding := range findings {
		result = append(result, finding.String())
	}

	return result
}

func writeFixtureFile(t *testing.T, root, name, contents string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(path, []byte(contents), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

const signalingAsyncAPI = `asyncapi: 2.6.0
info: {title: routegate signaling fixture, version: 0.1.0}
servers:
  ring: {host: api.example.test, protocol: wss}
channels:
  ping:
    address: /ws
    servers: [{$ref: '#/servers/ring'}]
    messages: {ping: {$ref: '#/components/messages/Ping'}}
  pong:
    address: /ws
    servers: [{$ref: '#/servers/ring'}]
    messages: {pong: {$ref: '#/components/messages/Pong'}}
operations:
  sendPing: {action: send, channel: {$ref: '#/channels/ping'}}
  receivePong: {action: receive, channel: {$ref: '#/channels/pong'}}
components:
  messages:
    Ping: {payload: {$ref: '#/components/schemas/PingFrame'}}
    Pong: {payload: {$ref: '#/components/schemas/PongFrame'}}
  schemas:
    PingFrame:
      type: object
      properties:
        method: {type: string, const: ping}
        body: {$ref: '#/components/schemas/PingBody'}
    PingBody: {type: object}
    PongFrame:
      type: object
      properties:
        method: {type: string, const: pong}
        body: {$ref: '#/components/schemas/PongBody'}
    PongBody: {type: object}
`

func replaceOnce(t *testing.T, source, before, after string) string {
	t.Helper()

	if !strings.Contains(source, before) {
		t.Fatalf("fixture does not contain mutation target %q", before)
	}

	return strings.Replace(source, before, after, 1)
}
