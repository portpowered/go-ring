package replay_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/stretchr/testify/require"
)

type syntheticChannelStep struct {
	Channel string          `json:"channel"`
	Kind    string          `json:"kind"`
	Frame   string          `json:"frame"`
	Body    json.RawMessage `json:"body"`
}

type syntheticChannelTranscript struct {
	Provenance string                 `json:"provenance"`
	Handshake  replay.WSHandshake     `json:"handshake"`
	Steps      []syntheticChannelStep `json:"steps"`
}

// A schema-derived synthetic transcript covers every built-in AsyncAPI
// channel. Historical sessions are excluded from this inventory.
func TestSyntheticSignalingChannelTranscript(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("fixtures", "signaling", "synthetic", "paired", "full-session.json"))
	require.NoError(t, err)

	var transcript syntheticChannelTranscript

	require.NoError(t, json.Unmarshal(data, &transcript))
	require.Contains(t, transcript.Provenance, "synthetic")
	doc := loadYAML(t, filepath.Join("..", "..", "api", "asyncapi.yaml"))
	channels := object(doc["channels"])
	// Account events use a separate WebSocket and paired transcript.
	delete(channels, "accountEvent")

	actions := map[string]string{}

	for _, raw := range object(doc["operations"]) {
		operation := object(raw)

		ref, _ := object(operation["channel"])["$ref"].(string)
		if ref == "" {
			continue
		}

		kind := "expect"
		if operation["action"] == "receive" {
			kind = "send"
		}

		actions[ref[len("#/channels/"):]] = kind
	}

	require.Len(t, transcript.Steps, len(channels))

	seen := map[string]bool{}

	steps := make([]replay.WSStep, 0, len(transcript.Steps))

	for _, step := range transcript.Steps {
		require.Contains(t, channels, step.Channel)
		require.False(t, seen[step.Channel], "duplicate channel")
		seen[step.Channel] = true

		require.Equal(t, actions[step.Channel], step.Kind)
		require.Equal(t, "text", step.Frame)
		steps = append(steps, replay.WSStep{Kind: step.Kind, Frame: step.Frame, Body: step.Body})
	}

	require.Len(t, seen, len(channels))
	require.Equal(t, "<loopback>", transcript.Handshake.Host)
	transcript.Handshake.Host = "" // exact runtime listener host, not a wildcard

	peer := replay.NewWebSocketServer(steps, 2*time.Second, transcript.Handshake)
	t.Cleanup(peer.Close)

	headers := transcript.Handshake.Headers.Clone()
	headers.Set("Origin", transcript.Handshake.Origin)

	conn, response, err := websocket.DefaultDialer.Dial(peer.URL()+"/ws?token=synthetic-ticket", headers)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	for _, step := range steps {
		if step.Kind == "expect" {
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, step.Body))

			continue
		}

		frame, body, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		require.Equal(t, websocket.TextMessage, frame)
		require.True(t, replay.SemanticEqual(step.Body, body))
	}

	require.NoError(t, peer.AssertComplete(3*time.Second))
}

// The inherited session files lack a verifiable capture date and upgrade
// request. Replay every application frame in order with an explicit synthetic
// loopback handshake; no selected-message subset can satisfy this test.
func TestHistoricalSignalingFullTranscripts(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			recording, err := replay.LoadSessionRecording(filepath.Join("fixtures", "signaling", "historical", name))
			require.NoError(t, err)
			require.NotEmpty(t, recording.Messages)

			steps := make([]replay.WSStep, 0, len(recording.Messages))

			for _, message := range recording.Messages {
				kind := "expect"
				if message.Direction == capturedServerToClientDirection {
					kind = "send"
				} else {
					require.Equal(t, capturedClientToServerDirection, message.Direction)
				}

				steps = append(steps, replay.WSStep{Kind: kind, Frame: message.Frame, Body: message.Payload})
			}

			peer := replay.NewWebSocketServer(
				steps,
				2*time.Second,
				replay.WSHandshake{Path: "/", Origin: "https://synthetic.example"},
			)
			t.Cleanup(peer.Close)

			conn, response, err := websocket.DefaultDialer.Dial(peer.URL(), http.Header{"Origin": {"https://synthetic.example"}})
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			require.NoError(t, err)

			t.Cleanup(func() { _ = conn.Close() })

			for _, step := range steps {
				if step.Kind == "expect" {
					require.NoError(t, conn.WriteMessage(websocket.TextMessage, step.Body))

					continue
				}

				frame, body, readErr := conn.ReadMessage()
				require.NoError(t, readErr)
				require.Equal(t, websocket.TextMessage, frame)
				require.True(t, replay.SemanticEqual(step.Body, body), "server frame differed from transcript")
			}

			require.NoError(t, peer.AssertComplete(3*time.Second))
		})
	}
}
