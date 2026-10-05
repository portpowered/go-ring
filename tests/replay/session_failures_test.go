package replay_test

import (
	"bytes"
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
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/ring"
)

const (
	negotiationCancelMode    = "negotiation_cancel"
	heartbeatTimeoutMode     = "heartbeat_timeout"
	eventBackpressureMode    = "event_backpressure"
	malformedRPCEnvelopeMode = "malformed_rpc_envelope"
	malformedCloseMode       = "malformed_close"
)

const (
	syntheticFailureDeviceID  = 1001
	syntheticFailureSessionID = "s"
	syntheticFailureRIID      = "r"
	heartbeatPeerReadTimeout  = 6 * time.Second
)

type multipleFailureFrameValuesError struct{}

func (multipleFailureFrameValuesError) Error() string {
	return "multiple JSON values in signaling frame"
}

func TestStartSessionRejectsInvalidOfferBeforeOpeningSession(t *testing.T) {
	t.Parallel()

	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ticket":"x"}`))
	}))
	t.Cleanup(tickets.Close)

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e == nil {
			defer func() { _ = c.Close() }()

			_, _, _ = c.ReadMessage()
		}
	}))
	t.Cleanup(ws.Close)

	url := "ws" + strings.TrimPrefix(ws.URL, "http")

	client, err := ring.NewClient(
		ring.WithHTTPClient(tickets.Client()),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}),
		ring.WithSignalingWebSocketURL(url),
	)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := client.OpenSignaling(
		context.Background(),
		ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "x", HardwareID: ""}},
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	for _, req := range []ring.StartDeviceSessionRequest{
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeAnswer, SDP: offerSDP}},
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: "not SDP"}},
		{DeviceID: "bad", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP}},
	} {
		{
			_, err := conn.StartDeviceSession(context.Background(), req)
			if err == nil {
				t.Fatalf("accepted invalid request: %+v", req)
			}
		}
	}
}

func TestOversizedTicketResponseIsRejected(t *testing.T) {
	t.Parallel()

	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	}))
	t.Cleanup(tickets.Close)

	client, err := ring.NewClient(
		ring.WithHTTPClient(tickets.Client()),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}),
		ring.WithSignalingWebSocketURL("ws://127.0.0.1/unused"),
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		_, err = client.OpenSignaling(
			context.Background(),
			ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "x", HardwareID: ""}},
		)
		if err == nil ||
			!strings.Contains(err.Error(), "size limit") {
			t.Fatalf("oversized ticket response error = %v", err)
		}
	}
}

func TestNegotiationCancellationAndPendingRPCFailure(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{
		negotiationCancelMode,
		"rpc_remote_close",
		heartbeatTimeoutMode,
		eventBackpressureMode,
		malformedRPCEnvelopeMode,
		malformedCloseMode,
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			tickets := httptest.NewServer(
				http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ticket":"x"}`)) },
				),
			)
			t.Cleanup(tickets.Close)

			up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			ready := make(chan struct{})
			peerDone := make(chan struct{})
			startReturned := make(chan struct{})

			var startReturnedOnce sync.Once

			signalStartReturned := func() { startReturnedOnce.Do(func() { close(startReturned) }) }

			ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				serveNegotiationFailurePeer(t, mode, up, ready, peerDone, startReturned, w, r)
			}))
			// Ensure malformed frames arrive after session startup succeeds.
			t.Cleanup(ws.Close)
			t.Cleanup(signalStartReturned)

			url := "ws" + strings.TrimPrefix(ws.URL, "http")

			client, err := ring.NewClient(
				ring.WithHTTPClient(tickets.Client()),
				ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}),
				ring.WithSignalingWebSocketURL(url),
			)
			if err != nil {
				t.Fatal(err)
			}

			conn, err := client.OpenSignaling(
				context.Background(),
				ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "x", HardwareID: ""}},
			)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = conn.Close() })

			runFailureScenario(t, mode, conn, ready, signalStartReturned)
		})
	}
}

func serveNegotiationFailurePeer(
	t *testing.T,
	mode string,
	up websocket.Upgrader,
	ready, peerDone, startReturned chan struct{},
	w http.ResponseWriter,
	r *http.Request,
) {
	t.Helper()

	connection, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	defer func() { _ = connection.Close() }()
	defer close(peerDone)

	_, messageBytes, err := connection.ReadMessage()
	if err != nil {
		return
	}

	var first struct {
		Method string `json:"method"`
		Dialog string `json:"dialog_id"`
	}

	jsonErr := json.Unmarshal(messageBytes, &first)
	if jsonErr != nil || first.Method != protocol.MethodLiveView {
		return
	}

	close(ready)

	if mode == negotiationCancelMode {
		_, _, _ = connection.ReadMessage()

		return
	}

	if !sendInitialFailureFrames(connection, mode, first.Dialog) {
		return
	}

	if mode == malformedRPCEnvelopeMode || mode == malformedCloseMode {
		<-startReturned

		if mode == malformedRPCEnvelopeMode {
			_ = writeFailureFrame(connection,
				map[string]any{
					"method":    "rpc",
					"dialog_id": first.Dialog,
					"riid":      "r",
					"body":      map[string]any{"doorbot_id": 1001, "session_id": "s", "command": "broken"},
				},
			)
		} else {
			_ = writeFailureFrame(connection,
				map[string]any{
					"method": "close", "dialog_id": first.Dialog, "riid": "r", "body": "malformed",
				},
			)
		}

		return
	}

	if mode == heartbeatTimeoutMode {
		consumeHeartbeatUntilClose(t, connection, first.Dialog)

		return
	}

	if mode == eventBackpressureMode {
		_, _, _ = connection.ReadMessage() // Wait until StartDeviceSession has returned.

		for eventIndex := range 40 {
			if writeFailureFrame(connection,
				map[string]any{
					"method":    "notification",
					"dialog_id": first.Dialog,
					"riid":      "r",
					"body": map[string]any{
						"doorbot_id": 1001, "session_id": "s", "is_ok": true,
						"text": "synthetic backpressure event", "sequence": eventIndex,
					},
				},
			) != nil {
				return
			}
		}

		return
	}

	_, _, e := connection.ReadMessage()
	if e != nil {
		return
	}
}

func consumeHeartbeatUntilClose(t *testing.T, connection *websocket.Conn, dialog string) {
	t.Helper()

	consumeFailureSessionUntilClose(t, connection, dialog, "heartbeat", heartbeatPeerReadTimeout, 2, false)
}

func consumeFailureSessionUntilClose(
	t *testing.T,
	connection *websocket.Conn,
	dialog, peerKind string,
	readTimeout time.Duration,
	minimumPings int,
	replyToPing bool,
) {
	t.Helper()

	err := connection.SetReadDeadline(time.Now().Add(readTimeout))
	if err != nil {
		t.Errorf("set %s peer read deadline: %v", peerKind, err)

		return
	}

	pings := 0

	for {
		_, msg, err := connection.ReadMessage()
		if err != nil {
			t.Errorf("%s peer disconnected before signaling close: %v", peerKind, err)

			return
		}

		var envelope signaling.Message

		decodeErr := decodeFailureFrame(msg, &envelope)
		if decodeErr != nil {
			t.Errorf("decode %s client message %s: %v", peerKind, msg, decodeErr)

			continue
		}

		switch envelope.Method {
		case protocol.MethodPing:
			var frame generatedsignaling.SessionPingFrame

			decodeErr := decodeFailureFrame(msg, &frame)
			if decodeErr != nil {
				t.Errorf("decode %s ping frame: %v", peerKind, decodeErr)

				continue
			}

			if assertSyntheticFailureSessionFrame(
				t,
				frame.Method, frame.DialogId, frame.Riid, frame.AdditionalProperties, frame.Body,
				protocol.MethodPing, dialog,
			) {
				pings++

				if replyToPing {
					writeErr := writeFailureFrame(connection,
						generatedsignaling.SessionPongFrame{
							Method:   protocol.MethodPong,
							DialogId: dialog,
							Riid:     syntheticFailureRIID,
							Body: &generatedsignaling.SessionBody{
								DoorbotId:            syntheticFailureDeviceID,
								SessionId:            syntheticFailureSessionID,
								AdditionalProperties: nil,
							},
							AdditionalProperties: nil,
						},
					)
					if writeErr != nil {
						t.Errorf("reply to %s heartbeat: %v", peerKind, writeErr)

						return
					}
				}
			}
		case protocol.MethodClose:
			var frame generatedsignaling.SessionCloseFrame

			decodeErr := decodeFailureFrame(msg, &frame)
			if decodeErr != nil {
				t.Errorf("decode %s close frame: %v", peerKind, decodeErr)
			} else {
				assertSyntheticFailureSessionFrame(
					t,
					frame.Method, frame.DialogId, frame.Riid, frame.AdditionalProperties, frame.Body,
					protocol.MethodClose, dialog,
				)
			}

			if pings < minimumPings {
				t.Errorf("%s closed after only %d pings", peerKind, pings)
			}

			// Keep the peer alive until the client has published the session error.
			waitForRecordedClientClose(t, connection)

			return
		default:
			t.Errorf("%s sent unexpected %q before close: %s", peerKind, envelope.Method, msg)
		}
	}
}

func decodeFailureFrame(message []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(message))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(destination)
	if err != nil {
		return wrapReplayTestError("decode signaling frame", err)
	}

	var trailing any

	err = decoder.Decode(&trailing)
	if err == nil {
		return multipleFailureFrameValuesError{}
	}

	if !errors.Is(err, io.EOF) {
		return wrapReplayTestError("decode trailing signaling frame data", err)
	}

	return nil
}

func assertSyntheticFailureSessionFrame(
	t *testing.T,
	gotMethod, gotDialog, gotRIID string,
	additionalProperties map[string]interface{},
	body *generatedsignaling.SessionBody,
	wantMethod, wantDialog string,
) bool {
	t.Helper()

	valid := true

	if gotMethod != wantMethod {
		t.Errorf("session frame method = %q, want %q", gotMethod, wantMethod)

		valid = false
	}

	if gotDialog != wantDialog {
		t.Errorf("session frame dialog = %q, want %q", gotDialog, wantDialog)

		valid = false
	}

	if gotRIID != syntheticFailureRIID {
		t.Errorf("session frame riid = %q, want %q", gotRIID, syntheticFailureRIID)

		valid = false
	}

	if len(additionalProperties) != 0 {
		t.Errorf("session frame has unexpected top-level properties: %v", additionalProperties)

		valid = false
	}

	if body == nil {
		t.Error("session frame body is missing")

		return false
	}

	if len(body.AdditionalProperties) != 0 {
		t.Errorf("session frame body has unexpected properties: %v", body.AdditionalProperties)

		valid = false
	}

	if body.DoorbotId != syntheticFailureDeviceID {
		t.Errorf("session frame doorbot_id = %d, want %d", body.DoorbotId, syntheticFailureDeviceID)

		valid = false
	}

	if body.SessionId != syntheticFailureSessionID {
		t.Errorf("session frame session_id = %q, want %q", body.SessionId, syntheticFailureSessionID)

		valid = false
	}

	return valid
}

func sendInitialFailureFrames(connection *websocket.Conn, mode, dialog string) bool {
	_ = writeFailureFrame(connection,
		map[string]any{
			"method":    protocol.MethodSessionCreated,
			"dialog_id": dialog,
			"riid":      syntheticFailureRIID,
			"body": map[string]any{
				"doorbot_id": syntheticFailureDeviceID, "session_id": syntheticFailureSessionID,
			},
		},
	)

	pingInterval := 10
	if mode == heartbeatTimeoutMode {
		pingInterval = 1
	}

	_ = writeFailureFrame(connection,
		map[string]any{
			"method":    protocol.MethodSDP,
			"dialog_id": dialog,
			"riid":      syntheticFailureRIID,
			"body": map[string]any{
				"doorbot_id": syntheticFailureDeviceID,
				"session_id": syntheticFailureSessionID,
				"type":       protocol.SDPTypeAnswer,
				"sdp":        answerSDP,
				"session_info": map[string]any{
					"session_id": "c", "ping_interval": pingInterval,
				},
			},
		},
	)
	for _, want := range []string{"activate_session", "mic_enable", "stream_options"} {
		_, msg, e := connection.ReadMessage()
		if e != nil || !strings.Contains(string(msg), want) {
			return false
		}
	}

	_ = writeFailureFrame(connection,
		map[string]any{
			"method":    protocol.MethodCameraStarted,
			"dialog_id": dialog,
			"riid":      syntheticFailureRIID,
			"body": map[string]any{
				"doorbot_id": syntheticFailureDeviceID, "session_id": syntheticFailureSessionID,
			},
		},
	)

	return true
}

func writeFailureFrame(connection *websocket.Conn, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return wrapReplayTestError("encode session failure frame", err)
	}

	err = connection.WriteMessage(websocket.TextMessage, raw)
	if err != nil {
		return wrapReplayTestError("write session failure frame", err)
	}

	return nil
}

func runFailureScenario(
	t *testing.T,
	mode string,
	conn *ring.SignalingConnection,
	ready <-chan struct{},
	signalStartReturned func(),
) {
	t.Helper()

	if mode == negotiationCancelMode {
		assertNegotiationCancel(t, conn, ready)

		return
	}

	request := ring.StartDeviceSessionRequest{
		DeviceID: "1001",
		Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
	}

	session, err := conn.StartDeviceSession(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	if mode == malformedRPCEnvelopeMode || mode == malformedCloseMode {
		signalStartReturned()
	}

	if mode == eventBackpressureMode {
		err = session.SetMicrophone(context.Background(), ring.SetMicrophoneRequest{Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
	}

	assertSessionFailureOutcome(t, mode, session)
}

func assertSessionFailureOutcome(t *testing.T, mode string, session *ring.DeviceSession) {
	t.Helper()

	if mode == heartbeatTimeoutMode || mode == eventBackpressureMode {
		waitLimit := time.Second
		want := ring.ErrSessionExpired
		state := ring.SessionExpired

		if mode == heartbeatTimeoutMode {
			waitLimit = 5 * time.Second
			want = ring.ErrSessionHeartbeat
			state = ring.SessionFailed
		}

		if mode == eventBackpressureMode {
			want = ring.ErrSessionBackpressure
			state = ring.SessionFailed
		}

		ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
		err := session.Wait(ctx)

		cancel()

		if !errors.Is(err, want) || session.State() != state {
			t.Fatalf("expired session state=%s err=%v", session.State(), err)
		}

		return
	}

	if mode == malformedRPCEnvelopeMode || mode == malformedCloseMode {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := session.Wait(ctx)

		cancel()

		if err == nil {
			t.Fatal("malformed remote message did not terminate session")
		}

		return
	}

	result := make(chan error, 1)

	go func() {
		_, e := session.PanStep(context.Background(), ring.PanStepRequest{Direction: "RIGHT"})
		result <- e
	}()

	select {
	case e := <-result:
		if e == nil {
			t.Fatal("pending RPC succeeded after peer closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending RPC was not released")
	}
}

func assertNegotiationCancel(t *testing.T, conn *ring.SignalingConnection, ready <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	go func() {
		_, sessionErr := conn.StartDeviceSession(
			ctx,
			ring.StartDeviceSessionRequest{
				DeviceID: "1001",
				Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
			},
		)
		result <- sessionErr
	}()

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("live_view was not sent")
	}

	cancel()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("negotiation result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("negotiation did not cancel")
	}
}
