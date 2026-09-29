package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const syntheticPanStepMethod = "PTZ.Pan.Step"

func TestSanitizerRedactsIdentifiersAndSDPSecrets(t *testing.T) {
	t.Parallel()

	input := decodeTestJSON(
		t,
		`{"method":"PTZ.Pan.Step","dialog_id":"di`+
			`alog-private","body":{"doorbot_id":98765`+
			`4321,"session_id":"signaling-private","c`+
			`ommand":{"id":"command-private","method"`+
			`:"PTZ.Pan.Step","params":{"sessionId":"c`+
			`ontrol-private","direction":"LEFT"}},"no`+
			`tification":{"text":"camera_connected","`+
			`email":"person@example.invalid","latitud`+
			`e":37.7},"sdp":"v=0\r\na=ice-ufrag:priva`+
			`te\r\na=ice-pwd:secretsecretsecretsecret`+
			`secret\r\na=candidate:1 1 UDP 1 10.2.3.4`+
			` 1234 typ host\r\na=fingerprint:sha-256 `+
			`AA:BB\r\n"}}`,
	)
	sanitized := newSanitizer().value(input, "")
	body := testObject(t, testValue(t, sanitized, "body"))
	command := testObject(t, testValue(t, body, "command"))
	params := testObject(t, testValue(t, command, "params"))

	notification := testObject(t, testValue(t, body, "notification"))

	if got := testValue(t, sanitized, "method"); got != syntheticPanStepMethod {
		t.Fatalf("method changed: %v", got)
	}

	if got := testValue(t, params, "direction"); got != "LEFT" {
		t.Fatalf("protocol enum changed: %v", got)
	}

	if got := testValue(t, body, "doorbot_id"); got != int64(1000) {
		t.Fatalf("device id was not redacted: %v", got)
	}

	if got := testValue(t, sanitized, "dialog_id"); got != "dialog-1" {
		t.Fatalf("dialog id was not redacted: %v", got)
	}

	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}

	serialized := string(encoded)
	for _, secret := range []string{"private", "10.2.3.4", "person@example.invalid"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("sanitized data contains %q", secret)
		}
	}

	sdp := testValueAs[string](t, body, "sdp").value
	if !strings.Contains(sdp, "syntheticufrag") || !strings.Contains(sdp, "192.0.2.1") {
		t.Fatalf("SDP candidate was not sanitized: %s", sdp)
	}

	if !strings.Contains(sdp, "a=fingerprint:sha-256 "+strings.TrimSuffix(strings.Repeat("00:", 32), ":")) {
		t.Fatalf("SDP fingerprint was not sanitized: %s", sdp)
	}

	if got := testValue(t, notification, "text"); got != cameraConnectedNotification {
		t.Fatalf("safe text enum changed: %v", got)
	}

	if got := testValue(t, notification, "latitude"); got != float64(0) {
		t.Fatalf("latitude was not redacted: %v", got)
	}
}

func TestSanitizerKeepsSeparateIdentifierDomainsAndIsRepeatable(t *testing.T) {
	t.Parallel()

	input := decodeTestJSON(t, `{"session_id":"same","dialog_id":"same","riid":"same","id":"same"}`)

	first, err := json.Marshal(newSanitizer().value(input, ""))
	if err != nil {
		t.Fatal(err)
	}

	second, err := json.Marshal(newSanitizer().value(input, ""))
	if err != nil {
		t.Fatal(err)
	}

	if string(first) != string(second) {
		t.Fatalf("sanitization changed between runs: %s != %s", first, second)
	}

	decoded := decodeTestJSON(t, string(first))
	values := []any{
		testValue(t, decoded, "session_id"),
		testValue(t, decoded, "dialog_id"),
		testValue(t, decoded, "riid"),
		testValue(t, decoded, "id"),
	}

	seen := make(map[any]struct{}, len(values))

	for _, value := range values {
		if _, exists := seen[value]; exists {
			t.Fatalf("separate identifier domains collided: %v", values)
		}

		seen[value] = struct{}{}
	}
}

func TestSanitizerScrubsNestedCredentialsAndLocationText(t *testing.T) {
	t.Parallel()

	input := decodeTestJSON(
		t,
		`{"extra":{"credentials":{"access_token":`+
			`"plain-secret-value","authorization":"Be`+
			`arer another-secret"},"where":"37.7749, `+
			`-122.4194","mailing_address":"42 Elm Str`+
			`eet, Exampletown","note":"api_key=privat`+
			`e-key-value","hardware_id":"hardware-ser`+
			`ial-value","timezone":"America/Los_Angel`+
			`es","location":"Home on Cedar Road"},"su`+
			`pported_rpc_commands":["PTZ.Pan.Step","P`+
			`TZ.Tilt.Continuous"]}`,
	)
	sanitized := newSanitizer().value(input, "")

	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}

	serialized := string(encoded)
	for _, secret := range []string{
		"plain-secret-value",
		"another-secret",
		"37.7749",
		"-122.4194",
		"Elm Street",
		"private-key-value",
		"hardware-serial-value",
		"America/Los_Angeles",
		"Cedar Road",
	} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("sanitized data contains %q", secret)
		}
	}

	commands := testValueAs[[]any](t, sanitized, "supported_rpc_commands").value
	if len(commands) != 2 || commands[0] != syntheticPanStepMethod || commands[1] != "PTZ.Tilt.Continuous" {
		t.Fatalf("supported RPC names changed: %v", commands)
	}
}

func TestSanitizerPreservesDeviceIDCrossReferencesAndPathVersions(t *testing.T) {
	t.Parallel()

	input := decodeTestJSON(t, `{"id":709739068,"device_id":709739068}`)

	sanitized := testObject(t, newSanitizer().value(input, ""))
	if gotID, gotDeviceID := testValue(t, sanitized, "id"), testValue(t, sanitized, "device_id"); gotID != gotDeviceID {
		t.Fatalf("device IDs did not retain their cross-reference: %v != %v", gotID, gotDeviceID)
	}

	if got := safePath("/devices/v1/devices/709739068/settings"); got != capturedSettingsPath {
		t.Fatalf("unexpected settings path: %s", got)
	}

	if got := safePath("/evm/v2/timeline/devices/709739068"); got != "/evm/v2/timeline/devices/{device_id}" {
		t.Fatalf("unexpected timeline path: %s", got)
	}
}
