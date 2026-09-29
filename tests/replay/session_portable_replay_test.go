package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
)

const (
	outboundICEScenario       = "outbound ICE"
	remoteICEScenario         = "remote ICE"
	remoteTerminationScenario = "remote termination"
)

type portableSessionCase struct {
	Case    string          `json:"case"`
	Message json.RawMessage `json:"message"`
}

type portableICEMessage struct {
	Method string          `json:"method"`
	Body   portableICEBody `json:"body"`
}

type portableICEBody struct {
	ICE   string `json:"ice"`
	MID   string `json:"mid"`
	Index int    `json:"mlineindex"`
}

type recordedSignalFrame struct {
	Body recordedSignalBody `json:"body"`
}

type recordedSignalBody struct {
	SessionID string `json:"session_id"`
}

func portableSessionCases(t *testing.T) map[string]json.RawMessage {
	t.Helper()

	rows, err := replay.LoadCases[portableSessionCase](
		filepath.Join("fixtures", "signaling", "synthetic", "session-variants.json"),
	)
	if err != nil {
		t.Fatal(err)
	}

	cases := make(map[string]json.RawMessage, len(rows))
	for _, row := range rows {
		cases[row.Case] = row.Message
	}

	return cases
}

func recordedLiveView(t *testing.T) (string, map[string]json.RawMessage) {
	t.Helper()

	recording, err := replay.LoadSessionRecording(filepath.Join("fixtures", "signaling", "historical", "flow-402.json"))
	if err != nil {
		t.Fatal(err)
	}

	var dialog, offer string

	messages := map[string]json.RawMessage{}

	for _, row := range recording.Messages {
		var envelope struct {
			Method string          `json:"method"`
			Dialog string          `json:"dialog_id"`
			Body   json.RawMessage `json:"body"`
		}

		err := json.Unmarshal(row.Payload, &envelope)
		if err != nil {
			t.Fatal(err)
		}

		if dialog == "" && envelope.Method == "live_view" && row.Direction == capturedClientToServerDirection {
			dialog = envelope.Dialog

			var body struct {
				SDP string `json:"sdp"`
			}

			err := json.Unmarshal(envelope.Body, &body)
			if err != nil {
				t.Fatal(err)
			}

			offer = body.SDP
		}

		if envelope.Dialog == dialog && row.Direction == capturedServerToClientDirection {
			switch envelope.Method {
			case "session_created", "sdp", "camera_started":
				messages[envelope.Method] = row.Payload
			}
		}
	}

	if offer == "" || len(messages) != 3 {
		t.Fatalf("incomplete recorded live_view: offer=%t messages=%d", offer != "", len(messages))
	}

	return offer, messages
}

// The real captured SDP and session envelopes are replayed independently of
// controls. Only the runtime dialog/device IDs are rebound for the local peer.
func recordedSessionFrame(t *testing.T, raw json.RawMessage, dialog string) map[string]any {
	t.Helper()

	var frame map[string]any

	err := json.Unmarshal(raw, &frame)
	if err != nil {
		t.Fatal(err)
	}

	frame["dialog_id"] = dialog

	body, ok := frame["body"].(map[string]any)
	if !ok {
		t.Fatalf("recorded session frame body has type %T", frame["body"])
	}

	body["doorbot_id"] = float64(1001)

	return frame
}

func TestRecordedLiveViewBehaviors(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)

	variants := portableSessionCases(t)

	for _, scenario := range []string{"establishment", outboundICEScenario, remoteICEScenario, remoteTerminationScenario} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
				serveRecordedLiveViewPeer(t, connection, dialog, captured, variants, scenario)
			})

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			t.Cleanup(cancel)

			session, err := conn.StartDeviceSession(
				ctx,
				ring.StartDeviceSessionRequest{
					DeviceID:     "1001",
					Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
					VideoEnabled: true,
					ICEMode:      ring.ICETrickle,
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			if session.Answer().Type != ring.SDPTypeAnswer || session.Answer().SDP == "" {
				t.Fatal("captured answer was not exposed")
			}

			assertRecordedLiveViewScenario(t, ctx, session, scenario)

			_ = session.Close()
			_ = conn.Close()
		})
	}
}

func assertRecordedLiveViewScenario(t *testing.T, ctx context.Context, session *ring.DeviceSession, scenario string) {
	t.Helper()

	switch scenario {
	case outboundICEScenario:
		err := session.SendICE(
			ctx,
			ring.ICECandidateRequest{Candidate: "candidate:01 synthetic", MID: "0", MLineIndex: 0},
		)
		if err != nil {
			t.Fatal(err)
		}
	case remoteICEScenario:
		for {
			event, err := session.Receive(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if event.Method == "ice" {
				break
			}
		}
	case remoteTerminationScenario:
		err := session.Wait(ctx)
		if !errors.Is(err, ring.ErrSessionClosed) || session.State() != ring.SessionClosed {
			t.Fatalf("remote close = %v, state %s", err, session.State())
		}
	default:
		if session.State() != ring.SessionActive {
			t.Fatal("session not active")
		}
	}
}

func serveRecordedLiveViewPeer(
	t *testing.T,
	connection *websocket.Conn,
	dialog string,
	captured, variants map[string]json.RawMessage,
	scenario string,
) {
	t.Helper()

	for _, method := range []string{"session_created", "sdp"} {
		err := connection.WriteJSON(recordedSessionFrame(t, captured[method], dialog))
		if err != nil {
			t.Error(err)

			return
		}
	}

	if !readActivation(connection) {
		t.Error("activation, microphone, or stream options missing")

		return
	}

	err := connection.WriteJSON(recordedSessionFrame(t, captured["camera_started"], dialog))
	if err != nil {
		t.Error(err)

		return
	}

	if !exchangeRecordedPeerScenario(t, connection, dialog, captured, variants, scenario) {
		return
	}

	for {
		var msg map[string]any
		if connection.ReadJSON(&msg) != nil || msg["method"] == "close" {
			return
		}
	}
}

func exchangeRecordedPeerScenario(
	t *testing.T,
	connection *websocket.Conn,
	dialog string,
	captured, variants map[string]json.RawMessage,
	scenario string,
) bool {
	t.Helper()

	switch scenario {
	case outboundICEScenario:
		var sent portableICEMessage

		err := connection.ReadJSON(&sent)
		if err != nil || sent.Method != "ice" || sent.Body.ICE != "candidate:01 synthetic" ||
			sent.Body.MID != "0" ||
			sent.Body.Index != 0 {
			t.Errorf("outbound ICE = %+v, %v", sent, err)

			return false
		}
	case remoteICEScenario, remoteTerminationScenario:
		var msg map[string]any

		err := json.Unmarshal(
			variants[map[string]string{remoteICEScenario: "remote-ice", remoteTerminationScenario: "remote-close"}[scenario]],
			&msg,
		)
		if err != nil {
			t.Error(err)

			return false
		}

		msg["dialog_id"] = dialog

		body, ok := msg["body"].(map[string]any)
		if !ok {
			t.Errorf("recorded remote frame body has type %T", msg["body"])

			return false
		}

		body["doorbot_id"] = float64(1001)

		body["session_id"] = recordedSignalID(t, captured["session_created"])

		err = connection.WriteJSON(msg)
		if err != nil {
			t.Error(err)

			return false
		}
	}

	return true
}

func recordedSignalID(t *testing.T, raw json.RawMessage) string {
	t.Helper()

	var frame recordedSignalFrame

	err := json.Unmarshal(raw, &frame)
	if err != nil {
		t.Fatal(err)
	}

	return frame.Body.SessionID
}
