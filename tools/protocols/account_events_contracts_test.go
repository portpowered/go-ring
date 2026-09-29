package protocols_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type accountEventHandshakeRequest struct {
	Method      string            `json:"method"`
	Origin      string            `json:"origin"`
	EscapedPath string            `json:"escaped_path"`
	Headers     map[string]string `json:"headers"`
}

type accountEventHandshakeResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
}

type accountEventHandshake struct {
	Request  accountEventHandshakeRequest  `json:"request"`
	Response accountEventHandshakeResponse `json:"response"`
}

type accountEventFrame struct {
	Direction string         `json:"direction"`
	Payload   map[string]any `json:"payload"`
}

type accountEventFixture struct {
	Provenance string                `json:"provenance"`
	Handshake  accountEventHandshake `json:"handshake"`
	Frames     []accountEventFrame   `json:"frames"`
}

func signalingChannels(channels map[string]any) map[string]any {
	selected := make(map[string]any)

	for name, raw := range channels {
		servers, ok := mapValue(raw)["servers"].([]any)
		if !ok {
			continue
		}

		for _, server := range servers {
			if stringValue(mapValue(server)["$ref"]) == "#/servers/ring" {
				selected[name] = raw
			}
		}
	}

	return selected
}

func TestSyntheticAccountEventPairMatchesAsyncAPI(t *testing.T) {
	t.Parallel()

	path := filepath.Join(
		repositoryRoot(t), "tests", "replay", "fixtures", "events", "synthetic", "paired", "account-event.json",
	)

	data, err := os.ReadFile(path) // #nosec G304 -- fixed repository-owned synthetic fixture.
	if err != nil {
		t.Fatal(err)
	}

	var fixture accountEventFixture

	err = json.Unmarshal(data, &fixture)
	if err != nil {
		t.Fatal(err)
	}

	if fixture.Provenance != "synthetic" || fixture.Handshake.Request.Origin != "$configuredEventOrigin" {
		t.Fatal("event fixture needs explicit synthetic provenance and origin matcher")
	}

	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	channels := mapValue(doc["channels"])
	channel := mapValue(channels["accountEvent"])
	bindings := mapValue(mapValue(channel["bindings"])["ws"])

	if stringValue(channel["address"]) != fixture.Handshake.Request.EscapedPath ||
		stringValue(bindings["method"]) != fixture.Handshake.Request.Method {
		t.Fatal("account event handshake method/path differs from AsyncAPI")
	}

	if fixture.Handshake.Response.Status != 101 ||
		!strings.EqualFold(fixture.Handshake.Response.Headers["Upgrade"], "websocket") ||
		!strings.EqualFold(fixture.Handshake.Response.Headers["Connection"], "Upgrade") {
		t.Fatal("account event fixture lacks a WebSocket upgrade response")
	}

	for name := range fixture.Handshake.Request.Headers {
		if mapValue(mapValue(bindings["headers"])["properties"])[name] == nil {
			t.Fatalf("handshake header %q is absent from AsyncAPI", name)
		}
	}

	components := mapValue(doc["components"])
	messages := mapValue(components["messages"])
	message := mapValue(mapValue(channel["messages"])["accountEvent"])
	reference := strings.TrimPrefix(stringValue(message["$ref"]), "#/components/messages/")
	payload := mapValue(mapValue(messages[reference])["payload"])
	validator := compileJSONSchema(t, map[string]any{"components": components}, payload)

	if len(fixture.Frames) == 0 {
		t.Fatal("account event pair has no server frame")
	}

	for _, frame := range fixture.Frames {
		if frame.Direction != "server_to_client" {
			t.Fatalf("unexpected account event direction %q", frame.Direction)
		}

		err = validator.Validate(frame.Payload)
		if err != nil {
			t.Fatalf("account event frame violates AsyncAPI: %v", err)
		}
	}

	for name, invalid := range map[string]map[string]any{
		"kind type":      {"kind": 123},
		"device ID type": {"device_id": "not-an-integer"},
		"timestamp type": {"timestamp": true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			validationErr := validator.Validate(invalid)
			if validationErr == nil {
				t.Fatal("invalid account event field type passed AsyncAPI validation")
			}
		})
	}
}
