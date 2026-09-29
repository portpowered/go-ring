package protocols_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/generatedsignaling"
)

func TestGeneratedStreamOptionsPreserveExplicitFalse(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value any
		field string
	}{
		{
			name: "audio only", field: `"audio_enabled":false`,
			value: generatedsignaling.SessionStreamAudioOptionsBody{
				DoorbotId: 1, SessionId: "s", AudioEnabled: false,
			},
		},
		{
			name: "video only", field: `"video_enabled":false`,
			value: generatedsignaling.SessionStreamVideoOptionsBody{
				DoorbotId: 1, SessionId: "s", VideoEnabled: false,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(encoded), test.field) {
				t.Fatalf("generated model dropped explicit false: %s", encoded)
			}
		})
	}
}

func TestGeneratedContinuousPTZPreservesStopSpeed(t *testing.T) {
	t.Parallel()

	direction := generatedsignaling.PtzDirectionLeft

	encoded, err := json.Marshal(generatedsignaling.PtzContinuousParams{
		SessionId: "session", Timestamp: 1, Version: 1, Direction: &direction,
		Speed: 0, Reason: "", AdditionalProperties: nil,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(encoded), `"speed":0`) {
		t.Fatalf("generated continuous PTZ model dropped stop speed: %s", encoded)
	}
}

func TestSessionCommandFramesRejectInvalidShapes(t *testing.T) {
	t.Parallel()

	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	components := mapValue(doc["components"])
	schemas := mapValue(components["schemas"])

	cases := []struct {
		name   string
		schema string
		frame  map[string]any
	}{
		{
			name:   "activate requires device ID",
			schema: "SessionActivateFrame",
			frame: map[string]any{
				"method": "activate_session", "dialog_id": "d", "body": map[string]any{"session_id": "s"},
			},
		},
		{
			name:   "microphone requires boolean",
			schema: "SessionMicrophoneFrame",
			frame: map[string]any{
				"method": "mic_enable", "dialog_id": "d",
				"body": map[string]any{"doorbot_id": 1, "session_id": "s", "enabled": "true"},
			},
		},
		{
			name:   "audio options require audio field",
			schema: "SessionStreamAudioOptionsFrame",
			frame: map[string]any{
				"method": "stream_options", "dialog_id": "d",
				"body": map[string]any{"doorbot_id": 1, "session_id": "s"},
			},
		},
		{
			name:   "video options require video field",
			schema: "SessionStreamVideoOptionsFrame",
			frame: map[string]any{
				"method": "stream_options", "dialog_id": "d",
				"body": map[string]any{"doorbot_id": 1, "session_id": "s"},
			},
		},
		{
			name:   "combined options require both fields",
			schema: "SessionStreamAudioVideoOptionsFrame",
			frame: map[string]any{
				"method": "stream_options", "dialog_id": "d",
				"body": map[string]any{"doorbot_id": 1, "session_id": "s"},
			},
		},
		{
			name:   "continuous PTZ requires speed",
			schema: "PTZContinuousCommandFrame",
			frame: map[string]any{
				"method": "rpc", "dialog_id": "d", "body": map[string]any{
					"doorbot_id": 1, "session_id": "s", "command": map[string]any{
						"jsonrpc": "2.0", "id": "c", "method": "PTZ.Pan.Continuous",
						"params": map[string]any{
							"sessionId": "s", "timestamp": 1, "version": 1, "direction": "LEFT",
						},
					},
				},
			},
		},
		{
			name:   "close requires the close method",
			schema: "SessionCloseFrame",
			frame: map[string]any{
				"method": "ping", "dialog_id": "d",
				"body": map[string]any{"doorbot_id": 1, "session_id": "s"},
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			validator := compileJSONSchema(t, map[string]any{"components": components}, mapValue(schemas[test.schema]))

			validationErr := validator.Validate(test.frame)
			if validationErr == nil {
				t.Fatal("invalid session command passed AsyncAPI validation")
			}
		})
	}
}
