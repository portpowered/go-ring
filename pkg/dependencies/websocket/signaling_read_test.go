package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/generatedsignaling"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/stretchr/testify/require"
)

func TestReadSignalingPreservesDecoderAndCloseCauses(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		frameType int
		payload   []byte
		check     func(error) bool
	}{
		{"invalid JSON", websocket.TextMessage, []byte(`{"method":`), func(err error) bool {
			var syntax *json.SyntaxError

			return errors.As(err, &syntax)
		}},
		{
			"socket close",
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
			func(err error) bool {
				var closeErr *websocket.CloseError

				return errors.As(err, &closeErr)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}

				defer func() { _ = peer.Close() }()

				_ = peer.WriteMessage(tc.frameType, tc.payload)
			}))
			defer server.Close()

			conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = conn.Close() }()

			err = ReadSignaling(conn, func(signaling.Message) { t.Fatal("unexpected valid message") })
			if !ringerrors.IsConnectionError(err) {
				t.Fatalf("read error is not typed: %v", err)
			}

			if !tc.check(err) {
				t.Fatalf("read error lost cause: %v", err)
			}
		})
	}
}

func TestReadSignalingSkipsBinaryFramesBeforeTextMessages(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer func() { _ = peer.Close() }()

		_ = peer.WriteMessage(websocket.BinaryMessage, []byte("not a signaling frame"))
		_ = peer.WriteMessage(
			websocket.TextMessage,
			[]byte(`{"method":"pong","dialog_id":"dialog-1","body":{"doorbot_id":1,"session_id":"session-1"}}`),
		)
	}))
	defer server.Close()

	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	var received signaling.Message

	err = ReadSignaling(conn, func(message signaling.Message) { received = message })
	if !ringerrors.IsConnectionError(err) {
		t.Fatalf("ReadSignaling terminal error = %v, want connection error after peer close", err)
	}

	if received.Method != "pong" || received.DialogID != "dialog-1" {
		t.Fatalf("text signaling frame after binary data = %#v", received)
	}
}

type failingSignalingDialer struct {
	err error
}

func (dialer failingSignalingDialer) DialContext(
	ctx context.Context,
	wsURL string,
	headers http.Header,
) (*websocket.Conn, *http.Response, error) {
	return nil, nil, dialer.err
}

func TestDialSignalingPreservesNetworkCause(t *testing.T) {
	t.Parallel()

	_, err := DialSignaling(
		context.Background(),
		"wss://example.invalid/signaling",
		http.Header{},
		failingSignalingDialer{err: io.ErrUnexpectedEOF},
	)
	if !ringerrors.IsNetworkError(err) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("signaling dial error lost its typed cause: %v", err)
	}
}

func TestValidateSignalingDialURLEnforcesModeledAndOverrideOrigins(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		wsURL   string
		wantErr bool
	}{
		{
			name: "modeled signaling URL",
			wsURL: "wss://api.prod.signalling.ring.devices.a2z.com/ws?api_version=4.0&" +
				"auth_type=ring_solutions&client_id=ring_site-test&token=synthetic",
			wantErr: false,
		},
		{
			name:    "modeled signaling URL with missing query",
			wsURL:   "wss://api.prod.signalling.ring.devices.a2z.com/ws?api_version=4.0",
			wantErr: true,
		},
		{name: "custom secure override", wsURL: "wss://example.invalid/events?region=test", wantErr: false},
		{name: "custom insecure override", wsURL: "ws://localhost:8080/events", wantErr: false},
		{name: "unsupported scheme", wsURL: "https://example.invalid/events", wantErr: true},
		{name: "malformed URL escape", wsURL: "wss://example.invalid/%gh", wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := validateSignalingDialURL(testCase.wsURL)
			if (err != nil) != testCase.wantErr {
				t.Fatalf("validateSignalingDialURL(%q) error = %v, want error %v", testCase.wsURL, err, testCase.wantErr)
			}
		})
	}
}

func TestOpenEventsRejectsInvalidSchemeBeforeDial(t *testing.T) {
	t.Parallel()

	connection, err := OpenEvents(context.Background(), "http://example.invalid/events", "", "")
	if connection != nil || !ringerrors.IsBadRequestError(err) {
		t.Fatalf("invalid event URL was not rejected: connection=%v error=%v", connection, err)
	}
}

func TestWriteSignalingPreservesContextCause(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var message signaling.Message

	err := WriteSignaling(ctx, nil, message)
	if !ringerrors.IsConnectionError(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("signaling cancellation lost its typed cause: %v", err)
	}
}

type receiveTriggerContext struct {
	done        chan struct{}
	triggerOnce sync.Once
	onDone      func()
}

func (ctx *receiveTriggerContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (ctx *receiveTriggerContext) Done() <-chan struct{} {
	ctx.triggerOnce.Do(ctx.onDone)

	return ctx.done
}

func (ctx *receiveTriggerContext) Err() error {
	select {
	case <-ctx.done:
		return context.Canceled
	default:
		return nil
	}
}

func (ctx *receiveTriggerContext) Value(any) any { return nil }

func TestReceivePreservesEventBeforeTerminalReadError(t *testing.T) {
	t.Parallel()

	expected := map[string]interface{}{"kind": "motion"}
	messageChan := make(chan accountEventRecord, 1)
	terminalErr := ringerrors.NewConnectionError("failed to read message", io.EOF)
	ctx := &receiveTriggerContext{
		done:        make(chan struct{}),
		triggerOnce: sync.Once{},
		onDone: func() {
			messageChan <- accountEventRecord{
				frame: generatedsignaling.AccountEventFrame{
					Kind:                 "",
					DeviceId:             0,
					Timestamp:            "",
					AdditionalProperties: nil,
				},
				raw: json.RawMessage(`{"kind":"motion"}`),
			}
		},
	}
	connection := &EventConnection{
		ctx:         ctx,
		messageChan: messageChan,
		errChan:     make(chan error, 1),
	}

	connection.errChan <- terminalErr

	// Done queues the event after Receive's initial nonblocking check, making
	// the message and terminal error both ready when its select runs.
	event, err := connection.Receive()
	require.NoError(t, err)
	require.Equal(t, expected, event)

	_, err = connection.Receive()
	require.Same(t, terminalErr, err)
	require.True(t, ringerrors.IsConnectionError(err))
	require.ErrorIs(t, err, io.EOF)
}

func TestReceiveDrainsEventsBeforePendingTerminalError(t *testing.T) {
	t.Parallel()

	terminalErr := ringerrors.NewConnectionError("failed to read message", io.EOF)

	connection := &EventConnection{
		pendingErr:  terminalErr,
		messageChan: make(chan accountEventRecord, 2),
		errChan:     make(chan error),
	}

	connection.messageChan <- accountEventRecord{
		frame: generatedsignaling.AccountEventFrame{Kind: "", DeviceId: 0, Timestamp: "", AdditionalProperties: nil},
		raw:   json.RawMessage(`{"sequence":1}`),
	}

	connection.messageChan <- accountEventRecord{
		frame: generatedsignaling.AccountEventFrame{Kind: "", DeviceId: 0, Timestamp: "", AdditionalProperties: nil},
		raw:   json.RawMessage(`{"sequence":2}`),
	}

	for sequence := float64(1); sequence <= 2; sequence++ {
		event, err := connection.Receive()
		require.NoError(t, err)
		require.InDelta(t, sequence, event["sequence"], 0)
	}

	_, err := connection.Receive()
	require.Same(t, terminalErr, err)
	require.ErrorIs(t, err, io.EOF)
}

func TestReceiveReturnsClosedForClosedConnectionOrChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		connection *EventConnection
	}{
		{name: "connection", connection: &EventConnection{closed: true}},
		{
			name: "message channel",
			connection: func() *EventConnection {
				messages := make(chan accountEventRecord)
				close(messages)

				return &EventConnection{messageChan: messages, errChan: make(chan error)}
			}(),
		},
		{
			name: "error channel",
			connection: func() *EventConnection {
				errors := make(chan error)
				close(errors)

				return &EventConnection{
					ctx: context.Background(), messageChan: make(chan accountEventRecord), errChan: errors,
				}
			}(),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := testCase.connection.Receive()
			require.True(t, ringerrors.IsClosedError(err), "receive error = %v", err)
		})
	}
}

func TestReceiveReturnsClosedAfterContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	connection := &EventConnection{
		ctx:         ctx,
		messageChan: make(chan accountEventRecord),
		errChan:     make(chan error),
	}

	_, err := connection.Receive()
	require.True(t, ringerrors.IsClosedError(err), "receive error = %v", err)
}

func TestReceiveReturnsTerminalReadErrorAfterContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	terminalErr := ringerrors.NewConnectionError("failed to read message", io.EOF)

	connection := &EventConnection{
		ctx:         ctx,
		messageChan: make(chan accountEventRecord),
		errChan:     make(chan error, 1),
	}
	connection.errChan <- terminalErr

	_, err := connection.Receive()
	require.Same(t, terminalErr, err)
	require.ErrorIs(t, err, io.EOF)
}

func TestReceivePendingErrorWithClosedMessageQueueReturnsClosed(t *testing.T) {
	t.Parallel()

	messages := make(chan accountEventRecord)
	close(messages)
	connection := &EventConnection{
		pendingErr:  io.EOF,
		messageChan: messages,
		errChan:     make(chan error),
	}

	_, err := connection.Receive()
	require.True(t, ringerrors.IsClosedError(err), "receive error = %v", err)
}

func TestPrioritizePendingEventReturnsTerminalErrorWhenNoEventIsQueued(t *testing.T) {
	t.Parallel()

	terminalErr := ringerrors.NewConnectionError("failed to read message", io.EOF)
	connection := &EventConnection{messageChan: make(chan accountEventRecord)}

	_, err := connection.prioritizePendingEvent(terminalErr)
	require.Same(t, terminalErr, err)
	require.ErrorIs(t, err, io.EOF)
}

func TestPrioritizePendingEventDefersErrorUntilQueuedEventIsReturned(t *testing.T) {
	t.Parallel()

	terminalErr := ringerrors.NewConnectionError("failed to read message", io.EOF)
	expected := map[string]interface{}{"sequence": float64(1)}
	connection := &EventConnection{messageChan: make(chan accountEventRecord, 1)}

	connection.messageChan <- accountEventRecord{
		frame: generatedsignaling.AccountEventFrame{
			Kind:                 "motion",
			DeviceId:             1000,
			Timestamp:            "2026-09-29T12:00:00Z",
			AdditionalProperties: nil,
		},
		raw: json.RawMessage(`{"sequence":1}`),
	}

	event, err := connection.prioritizePendingEvent(terminalErr)
	require.NoError(t, err)

	actual, decodeErr := decodeAccountEventPayload(event.raw)
	require.NoError(t, decodeErr)
	require.Equal(t, expected, actual)
	require.Same(t, terminalErr, connection.pendingErr)

	_, err = connection.Receive()
	require.Same(t, terminalErr, err)
	require.ErrorIs(t, err, io.EOF)
}

func TestPrioritizePendingEventReturnsClosedWhenMessageQueueIsClosed(t *testing.T) {
	t.Parallel()

	messages := make(chan accountEventRecord)
	close(messages)
	connection := &EventConnection{messageChan: messages}

	_, err := connection.prioritizePendingEvent(io.EOF)
	require.True(t, ringerrors.IsClosedError(err), "receive error = %v", err)
}
