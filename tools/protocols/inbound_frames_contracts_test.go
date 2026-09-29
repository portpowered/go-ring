package protocols_test

import (
	"path/filepath"
	"testing"
)

func TestGeneratedInboundFrameSchemasRejectMismatches(t *testing.T) {
	t.Parallel()

	doc := readYAMLObject(t, filepath.Join(repositoryRoot(t), "api", "asyncapi.yaml"))
	components := mapValue(doc["components"])
	schemas := mapValue(components["schemas"])

	cases := []struct {
		name    string
		schema  string
		valid   map[string]any
		invalid map[string]any
	}{
		{
			name: "camera started", schema: "ServerCameraStartedFrame",
			valid: map[string]any{
				"method": "camera_started", "dialog_id": "dialog", "body": map[string]any{
					"doorbot_id": 1000, "session_id": "session",
				},
			},
			invalid: map[string]any{
				"method": "camera_started", "dialog_id": "dialog", "body": map[string]any{
					"doorbot_id": "wrong", "session_id": "session",
				},
			},
		},
		{
			name: "remote close", schema: "ServerCloseFrame",
			valid: map[string]any{
				"method": "close", "dialog_id": "dialog", "body": map[string]any{
					"reason": map[string]any{"code": "session_closed", "text": "closed"},
				},
			},
			invalid: map[string]any{
				"method": "close", "dialog_id": "dialog", "body": map[string]any{
					"reason": map[string]any{"code": true},
				},
			},
		},
		{
			name: "remote ice", schema: "ServerICEFrame",
			valid: map[string]any{
				"method": "ice", "dialog_id": "dialog", "body": map[string]any{
					"ice": "candidate:synthetic", "mlineindex": 0,
				},
			},
			invalid: map[string]any{
				"method": "rpc", "dialog_id": "dialog", "body": map[string]any{
					"ice": "candidate:synthetic", "mlineindex": 0,
				},
			},
		},
		{
			name: "rpc result", schema: "ServerRPCFrame",
			valid: map[string]any{
				"method": "rpc", "dialog_id": "dialog", "body": map[string]any{
					"command": map[string]any{
						"jsonrpc": "2.0", "id": "command", "result": map[string]any{"sessionId": "control"},
					},
				},
			},
			invalid: map[string]any{
				"method": "rpc", "dialog_id": "dialog", "body": map[string]any{
					"command": map[string]any{"jsonrpc": "1.0", "id": "command"},
				},
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			validator := compileJSONSchema(t, map[string]any{"components": components}, mapValue(schemas[testCase.schema]))

			err := validator.Validate(testCase.valid)
			if err != nil {
				t.Fatalf("valid frame rejected: %v", err)
			}

			err = validator.Validate(testCase.invalid)
			if err == nil {
				t.Fatal("invalid frame passed schema validation")
			}
		})
	}
}
