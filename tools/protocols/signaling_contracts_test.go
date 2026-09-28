package protocols

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type capturedSignalingMessage struct {
	Direction string         `json:"direction"`
	Payload   map[string]any `json:"payload"`
}

type capturedSignalingFile struct {
	Messages []capturedSignalingMessage `json:"messages"`
}

type syntheticSignalingChannelStep struct {
	Channel string         `json:"channel"`
	Body    map[string]any `json:"body"`
}

type syntheticSignalingChannelTranscript struct {
	Steps []syntheticSignalingChannelStep `json:"steps"`
}

func TestSyntheticSignalingTranscriptMatchesEachAsyncAPIChannel(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), "tests", "replay", "fixtures", "signaling", "synthetic", "paired", "full-session.json")
	data, err := os.ReadFile(path) // #nosec G304 -- fixed repository-owned synthetic fixture.
	if err != nil {
		t.Fatal(err)
	}
	var transcript syntheticSignalingChannelTranscript
	if err := json.Unmarshal(data, &transcript); err != nil {
		t.Fatal(err)
	}
	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	channels := mapValue(doc["channels"])
	components := mapValue(doc["components"])
	messages := mapValue(components["messages"])
	seen := map[string]bool{}
	for _, step := range transcript.Steps {
		channel := mapValue(channels[step.Channel])
		if channel == nil || seen[step.Channel] {
			t.Fatalf("unknown or duplicate channel %q", step.Channel)
		}
		seen[step.Channel] = true
		var reference string
		for _, raw := range mapValue(channel["messages"]) {
			reference = stringValue(mapValue(raw)["$ref"])
		}
		const prefix = "#/components/messages/"
		if len(reference) <= len(prefix) || reference[:len(prefix)] != prefix {
			t.Fatalf("channel %s has invalid message reference %q", step.Channel, reference)
		}
		name := reference[len(prefix):]
		payload := mapValue(mapValue(messages[name])["payload"])
		validator := compileJSONSchema(t, map[string]any{"components": components}, payload)
		if err := validator.Validate(step.Body); err != nil {
			t.Errorf("%s synthetic frame violates AsyncAPI: %v", step.Channel, err)
		}
	}
	if len(seen) != len(channels) {
		t.Fatalf("covered %d of %d AsyncAPI channels", len(seen), len(channels))
	}
}

func capturedSignalingMessages(t *testing.T) []capturedSignalingMessage {
	t.Helper()
	pattern := filepath.Join(repositoryRoot(t), "tests", "replay", "fixtures", "signaling", "historical", "*.json")
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var messages []capturedSignalingMessage
	for _, path := range files {
		// #nosec G304 -- files are enumerated from the checked-in capture directory.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var file capturedSignalingFile
		if err := json.Unmarshal(data, &file); err != nil {
			t.Fatalf("parse signaling capture %s: %v", filepath.Base(path), err)
		}
		messages = append(messages, file.Messages...)
	}
	return messages
}

func findSignalingMessage(t *testing.T, messages []capturedSignalingMessage, match func(capturedSignalingMessage) bool) map[string]any {
	t.Helper()
	for _, message := range messages {
		if match(message) {
			return message.Payload
		}
	}
	t.Fatal("matching captured signaling message was not found")
	return nil
}

func TestRecordedSignalingPayloadsMatchAsyncAPISchemas(t *testing.T) {
	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	root := map[string]any{"components": doc["components"]}
	clientValidator := compileJSONSchema(t, root, map[string]any{"$ref": "#/components/schemas/ClientEnvelope"})
	serverValidator := compileJSONSchema(t, root, map[string]any{"$ref": "#/components/schemas/ServerEnvelope"})
	messages := capturedSignalingMessages(t)
	if len(messages) != 497 {
		t.Fatalf("captured signaling payload count = %d, want 497", len(messages))
	}
	for index, message := range messages {
		schemaName := "ClientEnvelope"
		if message.Direction == "server_to_client" {
			schemaName = "ServerEnvelope"
		} else if message.Direction != "client_to_server" {
			t.Fatalf("message %d has unknown direction %q", index, message.Direction)
		}
		method := stringValue(message.Payload["method"])
		validator := clientValidator
		if schemaName == "ServerEnvelope" {
			validator = serverValidator
		}
		if err := validator.Validate(message.Payload); err != nil {
			t.Errorf("message %d (%s, %s) violates %s: %v", index, message.Direction, method, schemaName, err)
		}
	}
}

func TestPTZNegativeVariantsAreRejected(t *testing.T) {
	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	validator := compileJSONSchema(t, map[string]any{"components": doc["components"]}, map[string]any{"$ref": "#/components/schemas/ClientEnvelope"})
	messages := capturedSignalingMessages(t)
	continuous := findSignalingMessage(t, messages, func(message capturedSignalingMessage) bool {
		return message.Direction == "client_to_server" && nestedMap(message.Payload, "body", "command")["method"] == "PTZ.Pan.Continuous"
	})
	variants := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing speed", func(value map[string]any) { delete(nestedMap(value, "body", "command", "params"), "speed") }},
		{"invalid direction", func(value map[string]any) { nestedMap(value, "body", "command", "params")["direction"] = "UP" }},
		{"invalid JSON-RPC version", func(value map[string]any) { nestedMap(value, "body", "command")["jsonrpc"] = "1.0" }},
		{"missing session id", func(value map[string]any) { delete(nestedMap(value, "body"), "session_id") }},
		{"invalid PTZ version", func(value map[string]any) { nestedMap(value, "body", "command", "params")["version"] = "1" }},
	}
	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			value := mapValue(cloneJSON(t, continuous))
			variant.mutate(value)
			if err := validator.Validate(value); err == nil {
				t.Fatal("invalid PTZ payload was accepted")
			}
		})
	}
}

func TestPTZSpeedBoundsAndExtensibleNotifications(t *testing.T) {
	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	root := map[string]any{"components": doc["components"]}
	clientValidator := compileJSONSchema(t, root, map[string]any{"$ref": "#/components/schemas/ClientEnvelope"})
	serverValidator := compileJSONSchema(t, root, map[string]any{"$ref": "#/components/schemas/ServerEnvelope"})
	messages := capturedSignalingMessages(t)
	continuous := findSignalingMessage(t, messages, func(message capturedSignalingMessage) bool {
		return message.Direction == "client_to_server" && nestedMap(message.Payload, "body", "command")["method"] == "PTZ.Pan.Continuous"
	})
	for _, testCase := range []struct {
		speed    float64
		accepted bool
	}{
		{-0.01, false}, {0, true}, {0.5, true}, {1, true}, {1.01, false},
	} {
		value := mapValue(cloneJSON(t, continuous))
		nestedMap(value, "body", "command", "params")["speed"] = testCase.speed
		err := clientValidator.Validate(value)
		if (err == nil) != testCase.accepted {
			t.Errorf("PTZ speed %v: accepted=%t, want %t (error %v)", testCase.speed, err == nil, testCase.accepted, err)
		}
	}

	event := findSignalingMessage(t, messages, func(message capturedSignalingMessage) bool {
		return stringValue(message.Payload["method"]) == "push_event"
	})
	future := mapValue(cloneJSON(t, event))
	nestedMap(future, "body")["notification_type"] = "future_event"
	if err := serverValidator.Validate(future); err != nil {
		t.Fatalf("future notification type was rejected: %v", err)
	}
	field := nestedMap(doc, "components", "schemas", "PushEventBody", "properties", "notification_type")
	if !containsString(sliceValue(field["x-extensible-enum"]), "shoulder_tap") {
		t.Fatal("known notification type is absent from x-extensible-enum")
	}
	if _, exists := field["enum"]; exists {
		t.Fatal("extensible notification type must not use a closed enum")
	}
}

func TestRPCResultAndErrorAreExclusive(t *testing.T) {
	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	validator := compileJSONSchema(t, map[string]any{"components": doc["components"]}, map[string]any{"$ref": "#/components/schemas/ServerEnvelope"})
	messages := capturedSignalingMessages(t)
	result := findSignalingMessage(t, messages, func(message capturedSignalingMessage) bool {
		return message.Direction == "server_to_client" && mapValue(nestedMap(message.Payload, "body", "command"))["result"] != nil
	})
	withError := mapValue(cloneJSON(t, result))
	command := nestedMap(withError, "body", "command")
	command["error"] = map[string]any{"code": -32602, "message": "synthetic error"}
	if err := validator.Validate(withError); err == nil {
		t.Fatal("RPC command with both result and error was accepted")
	}
	delete(command, "result")
	if err := validator.Validate(withError); err != nil {
		t.Fatalf("RPC error command was rejected: %v", err)
	}
}
