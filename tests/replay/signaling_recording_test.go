package replay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	mediavalidation "github.com/portpowered/go-ring/pkg/dependencies/webrtc"
)

type recordedMessages struct {
	Messages []recordedMessage `json:"messages"`
}

type recordedMessage struct {
	Direction string            `json:"direction"`
	Payload   signaling.Message `json:"payload"`
}

type recordedSDPFrame struct {
	Body recordedSDPFrameBody `json:"body"`
}

type recordedSDPFrameBody struct {
	SDP string `json:"sdp"`
}

type recordedRPCCommand struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type recordedRPCResultCommand struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
}

type recordedSessionRPC struct {
	DeviceID  int64              `json:"doorbot_id"`
	SessionID string             `json:"session_id"`
	Command   recordedRPCCommand `json:"command"`
}

type recordedSignalRPC struct {
	DeviceID int64              `json:"doorbot_id"`
	SignalID string             `json:"session_id"`
	Command  recordedRPCCommand `json:"command"`
}

type recordedRPCResult struct {
	Command recordedRPCResultCommand `json:"command"`
}

type recordedRPCBody struct {
	Command recordedRPCCommand `json:"command"`
}

// Each malformed SDP is a labeled mutation of a captured offer. The parser
// must reject ambiguous media identity before an ICE candidate can be routed.
func TestRecordedSDPIdentityFailureVariants(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)
	duplicateMID := "a=mid:0"

	for _, tc := range []struct {
		name string
		sdp  string
	}{
		{"missing MID", strings.Replace(offer, "a=mid:0", "a=mid:", 1)},
		{"duplicate MID", strings.Replace(offer, "a=mid:1", "a=mid:0", 1)},
		{"duplicate MID property", strings.Replace(offer, duplicateMID, duplicateMID+"\r\n"+duplicateMID, 1)},
		{"unknown bundle member", strings.Replace(offer, "a=group:BUNDLE 0", "a=group:BUNDLE unknown", 1)},
		{"conflicting session directions", strings.Replace(offer, "m=audio", "a=sendonly\r\na=recvonly\r\nm=audio", 1)},
		{"oversized offer", strings.Repeat("x", signaling.MaxMessageBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.sdp == offer {
				t.Fatal("fixture mutation did not change offer")
			}

			{
				_, err := mediavalidation.ParseSDP(tc.sdp)
				if err == nil {
					t.Fatal("ambiguous captured SDP mutation accepted")
				}
			}
		})
	}

	parsed, err := mediavalidation.ParseSDP(offer)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		mid   string
		index int
	}{
		{"0", -1}, {"0", 99}, {"unknown", 0},
	} {
		err := mediavalidation.ValidateICE(parsed, tc.mid, tc.index)
		if err == nil {
			t.Fatalf("invalid candidate media identity accepted: %+v", tc)
		}
	}

	var answerFrame recordedSDPFrame
	{
		err := json.Unmarshal(captured["sdp"], &answerFrame)
		if err != nil {
			t.Fatal(err)
		}
	}

	answer := answerFrame.Body.SDP

	lastMedia := strings.LastIndex(answer, "m=")

	for _, tc := range []struct {
		name string
		sdp  string
	}{
		{"fewer media sections", answer[:lastMedia]},
		{"changed media kind", strings.Replace(answer, "m=audio", "m=video", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			{
				_, err := mediavalidation.NormalizeAnswer(offer, tc.sdp)
				if err == nil {
					t.Fatal("incompatible answer accepted")
				}
			}
		})
	}
}

func loadConversation(t *testing.T, name string) recordedMessages {
	t.Helper()

	recordingBytes, err := os.ReadFile(
		filepath.Join("fixtures", "signaling", "historical", name),
	) // #nosec G304 -- name comes from fixed captured-recording cases in this test package.
	if err != nil {
		t.Fatal(err)
	}

	var recording recordedMessages
	{
		err = json.Unmarshal(recordingBytes, &recording)
		if err != nil {
			t.Fatal(err)
		}
	}

	return recording
}

// Python RingWebRtcStream supplies the SDP/ICE baseline. These assertions run
// the corresponding shapes from the Android conversations through our parser,
// including multiple same-kind media sections absent from the old Go example.
func TestRecordedSDPOfferAnswers(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assertRecordedSDPOfferAnswers(t, name)
		})
	}
}

func assertRecordedSDPOfferAnswers(t *testing.T, name string) {
	t.Helper()

	offers := make(map[string]string)
	answers := 0

	for _, row := range loadConversation(t, name).Messages {
		if validateRecordedSDPMessage(t, row, offers) {
			answers++
		}
	}

	if answers == 0 {
		t.Fatal("recording exercised no offer/answer pairs")
	}
}

func validateRecordedSDPMessage(t *testing.T, row recordedMessage, offers map[string]string) bool {
	t.Helper()

	var body struct {
		SDP string `json:"sdp"`
	}

	err := json.Unmarshal(row.Payload.Body, &body)
	if err != nil {
		t.Fatal(err)
	}

	if body.SDP == "" {
		return false
	}

	if row.Direction == capturedClientToServerDirection {
		_, err := mediavalidation.ParseSDP(body.SDP)
		if err != nil {
			t.Fatalf("%s offer: %v", row.Payload.Method, err)
		}

		offers[row.Payload.DialogID] = body.SDP

		return false
	}

	offer, ok := offers[row.Payload.DialogID]
	if !ok {
		t.Fatal("answer without corresponding offer")
	}

	_, err = mediavalidation.NormalizeAnswer(offer, body.SDP)
	if err != nil {
		t.Fatalf("answer: %v", err)
	}

	return true
}

// Captured ping/pong pairs exercise the timer and identity path one pair at a
// time; the separate virtual-hour test checks the hard 60-minute expiry.
func TestRecordedHeartbeatPairsIndividually(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		replayRecordedHeartbeatPairs(t, name)
	}
}

func replayRecordedHeartbeatPairs(t *testing.T, name string) {
	t.Helper()

	pending := make(map[string]signaling.Message)
	count := 0

	for _, row := range loadConversation(t, name).Messages {
		replayHeartbeatCaptureRow(t, name, row, pending, &count)
	}

	if count == 0 {
		t.Fatalf("%s has no recorded heartbeat pairs", name)
	}
}

func replayHeartbeatCaptureRow(
	t *testing.T,
	name string,
	row recordedMessage,
	pending map[string]signaling.Message,
	count *int,
) {
	t.Helper()

	message := row.Payload
	if message.Method == "ping" && row.Direction == capturedClientToServerDirection {
		pending[message.DialogID] = message

		return
	}

	if message.Method != "pong" || row.Direction != capturedServerToClientDirection {
		return
	}

	ping, ok := pending[message.DialogID]
	if !ok {
		return
	}

	delete(pending, message.DialogID)

	*count++
	t.Run(fmt.Sprintf("%s/pair-%02d", name, *count), func(t *testing.T) {
		t.Parallel()
		assertRecordedHeartbeatPair(t, ping, message)
	})
}

func assertRecordedHeartbeatPair(t *testing.T, ping, pong signaling.Message) {
	t.Helper()

	var body struct {
		DeviceID int64  `json:"doorbot_id"`
		SignalID string `json:"session_id"`
	}

	err := json.Unmarshal(ping.Body, &body)
	if err != nil {
		t.Fatal(err)
	}

	clock := newRecordedClock()
	out := make(chan signaling.Message, 1)

	session, err := signaling.NewSession(context.Background(), signaling.SessionConfig{
		DeviceID: body.DeviceID, DialogID: ping.DialogID, SignalID: body.SignalID,
		ControlID: "control-fixture", Heartbeat: 10 * time.Second, MaxAge: 0, Clock: clock,
		Send: func(_ context.Context, msg signaling.Message) error {
			out <- msg

			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = session.Close() })
	clock.advance()

	actual := recordedNextMessage(t, out)
	if actual.Method != "ping" || actual.DialogID != ping.DialogID || !replay.SemanticEqual(actual.Body, ping.Body) {
		t.Fatalf("ping differs from capture: %+v", actual)
	}

	err = session.Handle(pong)
	if err != nil {
		t.Fatal(err)
	}

	err = session.Send(context.Background(), "mic_enable", json.RawMessage(`{"enabled":true}`))
	if err != nil {
		t.Fatalf("matching pong did not keep session active: %v", err)
	}
}

// Incoming trickle ICE is an event on the matching signaling session, even
// when the capture's ICE belongs to a playback dialog rather than live_view.
func TestRecordedRemoteICEIndividually(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"flow-21.json", "flow-402.json"} {
		count := 0

		for _, row := range loadConversation(t, name).Messages {
			message := row.Payload
			if row.Direction != capturedServerToClientDirection || message.Method != "ice" {
				continue
			}

			count++
			t.Run(fmt.Sprintf("%s/candidate-%02d", name, count), func(t *testing.T) {
				t.Parallel()

				var body struct {
					DeviceID   int64  `json:"doorbot_id"`
					SignalID   string `json:"session_id"`
					Candidate  string `json:"ice"`
					MLineIndex int    `json:"mlineindex"`
				}

				{
					err := json.Unmarshal(message.Body, &body)
					if err != nil {
						t.Fatal(err)
					}
				}

				if body.Candidate == "" {
					t.Fatal("empty recorded ICE candidate")
				}

				session, err := signaling.NewSession(
					context.Background(),
					signaling.SessionConfig{
						DeviceID:  body.DeviceID,
						DialogID:  message.DialogID,
						SignalID:  body.SignalID,
						ControlID: "control-fixture",
						Heartbeat: 10 * time.Second,
						Clock:     newRecordedClock(),
						Send:      func(context.Context, signaling.Message) error { return nil },
					},
				)
				if err != nil {
					t.Fatal(err)
				}

				defer func() { _ = session.Close() }()

				{
					err := session.Handle(message)
					if err != nil {
						t.Fatal(err)
					}
				}

				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()

				event, err := session.Receive(ctx)
				if err != nil || event.Method != "ice" || !replay.SemanticEqual(event.Body, message.Body) {
					t.Fatalf("remote ICE event = %+v, %v", event, err)
				}
			})
		}

		if count == 0 {
			t.Fatalf("%s has no captured remote ICE", name)
		}
	}
}
