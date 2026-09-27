package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCaptureCLIExtractsSyntheticHTTPAndWebSocketFlows(t *testing.T) {
	working := t.TempDir()
	capturePath := filepath.Join(working, "capture.mitmproxy")
	outputPath := filepath.Join(working, "fixtures")
	if err := os.WriteFile(capturePath, syntheticCapture(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runCLI([]string{"-out", outputPath, capturePath}); err != nil {
		t.Fatal(err)
	}

	deviceListPath := filepath.Join(outputPath, "http", "captured", "device-list.json")
	deviceList := decodeFixtureFile(t, deviceListPath)
	request := testObject(t, testValue(t, deviceList, "request"))
	if got := testValue(t, request, "path"); got != "/device_info/v3/devices" {
		t.Fatalf("request path changed: %v", got)
	}
	query := testValue(t, request, "query").([]any)
	if len(query) != 1 || testValue(t, query[0], "name") != "access_token" || testValue(t, query[0], "value") != "opaque-1" {
		t.Fatalf("request query was not sanitized: %#v", query)
	}
	response := testObject(t, testValue(t, deviceList, "response"))
	body := testObject(t, testValue(t, response, "body"))
	if got := testValue(t, body, "device_id"); got != json.Number("1000") {
		t.Fatalf("device id was not replaced: %v", got)
	}
	variantPath := filepath.Join(outputPath, "http", "captured", "variants", "device-list-02.json")
	if _, err := os.Stat(variantPath); err != nil {
		t.Fatalf("additional response variant was not written: %v", err)
	}
	for _, flowNumber := range []int{21, 402} {
		path := filepath.Join(outputPath, "signaling", "captured", "flow-"+strconv.Itoa(flowNumber)+".json")
		session := decodeFixtureFile(t, path)
		messages := testValue(t, session, "messages").([]any)
		if len(messages) != 1 {
			t.Fatalf("flow %d has %d messages, want 1", flowNumber, len(messages))
		}
		message := testObject(t, messages[0])
		if testValue(t, message, "direction") != "client_to_server" || testValue(t, message, "frame") != "text" {
			t.Fatalf("unexpected WebSocket message shape: %#v", message.values)
		}
		payload := testObject(t, testValue(t, message, "payload"))
		if testValue(t, payload, "session_id") != "session-1" {
			t.Fatalf("session id was not sanitized: %#v", payload.values)
		}
	}
	for _, relative := range []string{
		filepath.Join("http", "captured", "device-list.json"),
		filepath.Join("http", "captured", "variants", "device-list-02.json"),
		filepath.Join("signaling", "captured", "flow-21.json"),
		filepath.Join("signaling", "captured", "flow-402.json"),
	} {
		// #nosec G304 -- relative names are fixed test outputs in a temporary directory.
		contents, err := os.ReadFile(filepath.Join(outputPath, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private-token", "device-secret", "session-private"} {
			if strings.Contains(string(contents), secret) {
				t.Fatalf("generated fixture %s contains private data", relative)
			}
		}
	}
}

func TestSafePathAndEligibleCoverCapturedRoutes(t *testing.T) {
	cases := []struct {
		host   string
		method string
		path   string
		want   string
	}{
		{"api.ring.com", "GET", "/device_info/v3/devices", "device-list"},
		{"api.ring.com", "GET", "/device_info/v3/devices/123", "device-detail"},
		{"api.ring.com", "PATCH", "/devices/v1/devices/123/settings", "device-settings-patch"},
		{"api.ring.com", "POST", "/clients_api/doorbots/123/siren_on", "siren-on"},
		{"api.ring.com", "GET", "/evm/v3/history/devices", "history-devices"},
		{"api.ring.com", "GET", "/evm/v2/timeline/devices/123", "device-timeline"},
		{"api.ring.com", "GET", "/location_info/v3/locations", "location-list"},
		{"api.ring.com", "GET", "/location_info/v4/locations/123", "location-detail"},
		{"api.ring.com", "GET", "/groups/v1/locations/123/groups", "groups"},
		{"api.ring.com", "GET", "/groups/v1/locations/123/devices", "group-devices"},
		{"api.ring.com", "PUT", "/clients_api/dings/123/favorite", "recording-favorite"},
		{"api.ring.com", "DELETE", "/clients_api/dings/123", "recording-delete"},
		{"api.ring.com", "POST", "/commands/v1/devices/123", "device-reboot"},
		{"api.ring.com", "PATCH", "/duos/v1/devices/123/update", "duos-update"},
		{"prd-api-us.prd.rings.solutions", "GET", "/api/v1/clap/tickets", "bootstrap-ticket"},
	}
	for _, test := range cases {
		flow := &capturedFlow{
			request:  &capturedMessage{method: test.method, host: test.host, path: test.path},
			response: &capturedResponse{status: 200},
		}
		if got := eligible(flow); got != test.want {
			t.Errorf("eligible(%s %s %s) = %q, want %q", test.method, test.host, test.path, got, test.want)
		}
	}
	if got := safePath("/clients_api/dings/recording-private/favorite"); got != "/clients_api/dings/{recording_id}/favorite" {
		t.Fatalf("recording identifier was not templated: %s", got)
	}
}

func syntheticCapture(t *testing.T) []byte {
	t.Helper()
	flows := make([]byte, 0)
	for flowNumber := 1; flowNumber <= 402; flowNumber++ {
		state := map[string]any{"version": int64(21), "type": "http"}
		if flowNumber == 1 || flowNumber == 2 {
			responseBody := `{"device_id":777,"serial":"device-secret"}`
			if flowNumber == 2 {
				responseBody = `{"device_id":778,"status":"another-status","extra":true}`
			}
			state["request"] = map[string]any{
				"method": []byte("GET"), "host": "api.ring.com", "path": []byte("/device_info/v3/devices?access_token=private-token"),
				"headers": []any{[]any{[]byte("Accept"), []byte("application/json")}}, "content": []byte{},
			}
			state["response"] = map[string]any{
				"status_code": int64(200), "headers": []any{[]any{[]byte("Content-Type"), []byte("application/json")}}, "content": []byte(responseBody),
			}
		}
		if flowNumber == 21 || flowNumber == 402 {
			state["websocket"] = map[string]any{
				"messages": []any{[]any{int64(1), true, []byte(`{"session_id":"session-private"}`), float64(1), false, false}},
			}
		}
		flows = append(flows, encodeTestTNetstring(t, state)...)
	}
	return flows
}

func decodeFixtureFile(t *testing.T, path string) *orderedObject {
	t.Helper()
	// #nosec G304 -- fixture paths are fixed test outputs or checked-in fixtures.
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeJSONOrdered(contents)
	if err != nil {
		t.Fatal(err)
	}
	return testObject(t, value)
}
