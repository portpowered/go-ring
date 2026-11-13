package replay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

// Ring sends informational frames that go-ring does not model, such as stream_info. One must
// reach the session as an event and leave it live, instead of ending every session on the connection.
func TestRecordedSessionReceivesUnmodeledFramesAndStaysLive(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)

	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, connection, dialog, captured, true) {
			return
		}

		err := connection.WriteJSON(map[string]any{
			"method":    "stream_info",
			"dialog_id": dialog,
			"body": map[string]any{
				"doorbot_id": 1001,
				"session_id": recordedSignalID(t, captured["session_created"]),
				"stream":     map[string]any{"bitrate": 512},
			},
		})
		if err != nil {
			t.Error(err)

			return
		}

		_, _, _ = connection.ReadMessage()
	})
	session := startRecordedSession(t, conn, offer)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for {
		event, err := session.Receive(ctx)
		if err != nil {
			t.Fatalf("session ended instead of returning stream_info: %v", err)
		}

		if event.Method != "stream_info" {
			continue
		}

		var body map[string]json.RawMessage

		var stream map[string]int

		if json.Unmarshal(event.Body, &body) != nil || json.Unmarshal(body["stream"], &stream) != nil ||
			stream["bitrate"] != 512 {
			t.Fatalf("stream_info body = %s", event.Body)
		}

		break
	}

	if session.State() != ring.SessionActive {
		t.Fatalf("session state after stream_info = %v", session.State())
	}
}
