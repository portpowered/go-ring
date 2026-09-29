package websocket_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
	ringwebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
)

type liveAnswerFailureCase struct {
	name   string
	frames []signaling.Message
	closed bool
}

func TestAwaitLiveAnswerRejectsMalformedSessionCreated(t *testing.T) {
	t.Parallel()

	tests := []liveAnswerFailureCase{
		{
			name:   "malformed session_created JSON",
			frames: []signaling.Message{sessionCreatedTestMessage(`{`)},
			closed: false,
		},
		{
			name:   "wrong device identity",
			frames: []signaling.Message{sessionCreatedTestMessage(`{"doorbot_id":999,"session_id":"signal"}`)},
			closed: false,
		},
		{
			name:   "missing signaling session identifier",
			frames: []signaling.Message{sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":""}`)},
			closed: false,
		},
		{
			name:   "answer before session_created",
			frames: []signaling.Message{liveAnswerTestMessage(`{}`)},
			closed: false,
		},
		{
			name: "peer closes during negotiation",
			frames: []signaling.Message{{
				Method:   protocol.MethodClose,
				DialogID: "dialog",
				RIID:     "",
				Body:     nil,
			}},
			closed: true,
		},
		{
			name: "conflicting duplicate session_created",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal-1"}`),
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal-2"}`),
			},
			closed: false,
		},
	}

	runLiveAnswerFailureCases(t, tests)
}

func TestAwaitLiveAnswerRejectsMalformedAnswers(t *testing.T) {
	t.Parallel()

	tests := []liveAnswerFailureCase{
		{
			name:   "answer before session_created",
			frames: []signaling.Message{liveAnswerTestMessage(`{}`)},
			closed: false,
		},
		{
			name: "peer closes during negotiation",
			frames: []signaling.Message{{
				Method:   protocol.MethodClose,
				DialogID: "dialog",
				RIID:     "",
				Body:     nil,
			}},
			closed: true,
		},
		{
			name: "invalid SDP JSON",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
				liveAnswerTestMessage(`{`),
			},
			closed: false,
		},
		{
			name: "invalid session_info shape",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
				liveAnswerTestMessageWithInfo("signal", "answer", json.RawMessage(`[]`)),
			},
			closed: false,
		},
		{
			name: "fractional heartbeat",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
				liveAnswerTestMessageWithInfo(
					"signal",
					"answer",
					json.RawMessage(`{"session_id":"control","ping_interval":1.5}`),
				),
			},
			closed: false,
		},
		{
			name: "string heartbeat",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
				liveAnswerTestMessageWithInfo(
					"signal",
					"answer",
					json.RawMessage(`{"session_id":"control","ping_interval":"10"}`),
				),
			},
			closed: false,
		},
		{
			name: "heartbeat exceeds maximum",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
				liveAnswerTestMessageWithInfo(
					"signal",
					"answer",
					json.RawMessage(`{"session_id":"control","ping_interval":61}`),
				),
			},
			closed: false,
		},
		{
			name: "SDP answer has wrong type",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
				liveAnswerTestMessageWithInfo(
					"signal",
					"offer",
					json.RawMessage(`{"session_id":"control"}`),
				),
			},
			closed: false,
		},
		{
			name: "SDP answer session mismatch",
			frames: []signaling.Message{
				sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
				liveAnswerTestMessageWithInfo(
					"other",
					"answer",
					json.RawMessage(`{"session_id":"control"}`),
				),
			},
			closed: false,
		},
	}

	runLiveAnswerFailureCases(t, tests)
}

func runLiveAnswerFailureCases(t *testing.T, tests []liveAnswerFailureCase) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := awaitLiveAnswerTestMessages(test.frames)
			if test.closed {
				if !ringerrors.IsClosedError(err) {
					t.Fatalf("negotiation error = %v, want closed error", err)
				}

				return
			}

			if !ringerrors.IsConnectionError(err) {
				t.Fatalf("negotiation error = %v, want connection error", err)
			}
		})
	}
}

func TestAwaitLiveAnswerUsesDefaultHeartbeatWhenPeerOmitsInterval(t *testing.T) {
	t.Parallel()

	frames := []signaling.Message{
		sessionCreatedTestMessage(`{"doorbot_id":1001,"session_id":"signal"}`),
		liveAnswerTestMessageWithInfo(
			"signal",
			"answer",
			json.RawMessage(`{"session_id":"control"}`),
		),
	}

	negotiation, err := awaitLiveAnswerTestMessages(frames)
	if err != nil {
		t.Fatalf("valid negotiation failed: %v", err)
	}

	if negotiation.SignalID != "signal" || negotiation.ControlID != "control" || negotiation.AnswerSDP != "v=0" {
		t.Fatalf("negotiation result = %#v", negotiation)
	}

	if negotiation.Heartbeat != signaling.DefaultHeartbeatInterval {
		t.Fatalf("default heartbeat = %s, want %s", negotiation.Heartbeat, signaling.DefaultHeartbeatInterval)
	}
}

func awaitLiveAnswerTestMessages(messages []signaling.Message) (ringwebsocket.LiveNegotiation, error) {
	events := make(chan signaling.Message, len(messages))
	for _, message := range messages {
		events <- message
	}

	negotiation, err := ringwebsocket.AwaitLiveAnswer(
		context.Background(),
		make(chan struct{}),
		func() error { return nil },
		events,
		1001,
		func() error { return context.DeadlineExceeded },
	)
	if err != nil {
		return ringwebsocket.LiveNegotiation{}, liveNegotiationTestError{cause: err}
	}

	return negotiation, nil
}

type liveNegotiationTestError struct {
	cause error
}

func (testError liveNegotiationTestError) Error() string {
	return "await test live answer: " + testError.cause.Error()
}

func (testError liveNegotiationTestError) Unwrap() error {
	return testError.cause
}

func sessionCreatedTestMessage(body string) signaling.Message {
	return signaling.Message{
		Method:   protocol.MethodSessionCreated,
		DialogID: "dialog",
		RIID:     "",
		Body:     json.RawMessage(body),
	}
}

func liveAnswerTestMessage(body string) signaling.Message {
	return signaling.Message{
		Method:   protocol.MethodSDP,
		DialogID: "dialog",
		RIID:     "",
		Body:     json.RawMessage(body),
	}
}

func liveAnswerTestMessageWithInfo(sessionID, answerType string, sessionInfo json.RawMessage) signaling.Message {
	body, err := json.Marshal(map[string]any{
		"doorbot_id":   int64(1001),
		"session_id":   sessionID,
		"type":         answerType,
		"sdp":          "v=0",
		"session_info": sessionInfo,
	})
	if err != nil {
		panic(err)
	}

	return liveAnswerTestMessage(string(body))
}
