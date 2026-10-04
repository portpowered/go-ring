package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

// These are synthetic mutations of the recorded signaling envelope shapes.
func identityPeer(t *testing.T, script func(*websocket.Conn, string)) *ring.SignalingConnection {
	t.Helper()

	return openRecordedPeer(t, func(connection *websocket.Conn) {
		var first struct {
			Method string `json:"method"`
			Dialog string `json:"dialog_id"`
		}

		err := connection.ReadJSON(&first)
		if err != nil {
			return
		}

		if first.Method != liveViewMethod {
			t.Errorf("unexpected initial method %s", first.Method)

			return
		}

		script(connection, first.Dialog)
	})
}

func openRecordedPeer(t *testing.T, script func(*websocket.Conn)) *ring.SignalingConnection {
	t.Helper()

	connection, _ := openRecordedPeerWithDone(t, script)

	return connection
}

func openRecordedPeerWithDone(
	t *testing.T,
	script func(*websocket.Conn),
) (*ring.SignalingConnection, <-chan struct{}) {
	t.Helper()

	serverDone := make(chan struct{})

	var serverDoneOnce sync.Once

	tickets := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ticket":"synthetic"}`)) },
		),
	)
	t.Cleanup(tickets.Close)

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer serverDoneOnce.Do(func() { close(serverDone) })

		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}

		defer func() { _ = connection.Close() }()

		_ = connection.SetReadDeadline(time.Now().Add(4 * time.Second))
		script(connection)
	}))
	t.Cleanup(peer.Close)

	client, err := ring.NewClient(
		ring.WithHTTPClient(tickets.Client()),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}),
		ring.WithSignalingWebSocketURL("ws"+strings.TrimPrefix(peer.URL, "http")),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	t.Cleanup(cancel)

	conn, err := client.OpenSignaling(
		ctx,
		ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "synthetic", HardwareID: ""}},
	)
	if err != nil {
		t.Fatal(err)
	}

	return conn, serverDone
}

func waitForRecordedClientClose(t *testing.T, connection *websocket.Conn) {
	t.Helper()

	for {
		messageType, _, err := connection.ReadMessage()
		if err != nil {
			var timeoutError net.Error
			if errors.As(err, &timeoutError) && timeoutError.Timeout() {
				t.Errorf("client did not close replay websocket before deadline: %v", err)
			}

			return
		}

		t.Errorf("unexpected client websocket frame type %d after replay exchange", messageType)
	}
}

func writeIdentity(c *websocket.Conn, dialog, method string, body any) error {
	return writeIdentityWithRIID(c, dialog, method, "", body)
}

func writeIdentityWithRIID(connection *websocket.Conn, dialog, method, riid string, body any) error {
	frame := map[string]any{"method": method, "dialog_id": dialog, "body": body}
	if riid != "" {
		frame["riid"] = riid
	}

	err := connection.WriteJSON(frame)
	if err != nil {
		return wrapReplayTestError("write signaling identity frame", err)
	}

	return nil
}
func beginIdentity(connection *websocket.Conn, dialog, riid string, interval any, include bool) {
	_ = writeIdentityWithRIID(
		connection,
		dialog,
		"session_created",
		riid,
		map[string]any{"doorbot_id": 1001, "session_id": "s"},
	)

	info := map[string]any{"session_id": "c"}
	if include {
		info["ping_interval"] = interval
	}

	_ = writeIdentityWithRIID(
		connection,
		dialog,
		"sdp",
		riid,
		map[string]any{"doorbot_id": 1001, "session_id": "s", "type": "answer", "sdp": answerSDP, "session_info": info},
	)
}

func assertIdentityCloseFrame(t *testing.T, frame map[string]any, dialog, riid string) {
	t.Helper()
	assertExactIdentityMapKeys(t, frame, "close frame", "method", "dialog_id", "riid", "body")

	if frame["dialog_id"] != dialog {
		t.Errorf("close dialog = %v, want %s", frame["dialog_id"], dialog)
	}

	if frame["riid"] != riid {
		t.Errorf("close riid = %v, want %s", frame["riid"], riid)
	}

	body, ok := frame["body"].(map[string]any)
	if !ok {
		t.Errorf("close body = %T, want object", frame["body"])

		return
	}

	assertExactIdentityMapKeys(t, body, "close body", "doorbot_id", "session_id")

	if body["doorbot_id"] != float64(1001) || body["session_id"] != "s" {
		t.Errorf("close body identity = %v, want doorbot_id 1001 and session_id s", body)
	}
}

func assertExactIdentityMapKeys(t *testing.T, value map[string]any, label string, expected ...string) {
	t.Helper()

	if len(value) != len(expected) {
		t.Errorf("%s has %d fields, want exactly %v", label, len(value), expected)
	}

	for _, key := range expected {
		if _, ok := value[key]; !ok {
			t.Errorf("%s is missing %q", label, key)
		}
	}
}
func readActivation(connection *websocket.Conn) bool {
	for _, method := range []string{"activate_session", "mic_enable", "stream_options"} {
		var message struct {
			Method string `json:"method"`
		}

		if connection.ReadJSON(&message) != nil || message.Method != method {
			return false
		}
	}

	return true
}

func assertInvalidHeartbeatResult(
	t *testing.T,
	conn *ring.SignalingConnection,
	session *ring.DeviceSession,
	err error,
	wantErrorText string,
	wantConnectionErrorText string,
) {
	t.Helper()

	if session != nil || err == nil {
		t.Fatalf("invalid interval accepted: session=%v err=%v", session != nil, err)
	}

	if wantErrorText != "" && !strings.Contains(err.Error(), wantErrorText) {
		t.Fatalf("invalid interval error = %v, want it to contain %q", err, wantErrorText)
	}

	if wantConnectionErrorText != "" {
		connectionErr := conn.Err()
		if connectionErr == nil || !strings.Contains(connectionErr.Error(), wantConnectionErrorText) {
			t.Fatalf("signaling connection error = %v, want it to contain %q", connectionErr, wantConnectionErrorText)
		}
	}
}

func TestNegotiatedHeartbeatRejectsInvalidPresentValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value                   any
		wantErrorText           string
		wantConnectionErrorText string
		wantClose               bool
	}{
		{value: 0, wantErrorText: "heartbeat interval", wantConnectionErrorText: "", wantClose: true},
		{value: -1, wantErrorText: "heartbeat interval", wantConnectionErrorText: "", wantClose: true},
		{value: 61, wantErrorText: "heartbeat interval", wantConnectionErrorText: "", wantClose: true},
		{value: 1.5, wantErrorText: "", wantConnectionErrorText: "invalid signaling message", wantClose: false},
		{value: "10", wantErrorText: "", wantConnectionErrorText: "invalid signaling message", wantClose: false},
		{value: nil, wantErrorText: "heartbeat interval", wantConnectionErrorText: "", wantClose: true},
	} {
		value := test.value

		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}

		t.Run(string(encoded), func(t *testing.T) {
			t.Parallel()

			conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
				const routeID = "heartbeat-route"

				beginIdentity(connection, dialog, routeID, value, true)

				if test.wantClose {
					closeRequest := readSignalRequest(t, connection, "close")
					if closeRequest == nil {
						return
					}

					assertIdentityCloseFrame(t, closeRequest, dialog, routeID)
				}

				waitForRecordedClientClose(t, connection)
			})

			t.Cleanup(func() { _ = conn.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			session, err := conn.StartDeviceSession(
				ctx,
				ring.StartDeviceSessionRequest{
					DeviceID: "1001",
					Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
				},
			)
			assertInvalidHeartbeatResult(t, conn, session, err, test.wantErrorText, test.wantConnectionErrorText)
		})
	}
}
func TestWrongIdentityCannotDeclareSessionReady(t *testing.T) {
	t.Parallel()

	for _, wrong := range []string{"device", "session"} {
		t.Run(wrong, func(t *testing.T) {
			t.Parallel()

			conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
				beginIdentity(connection, dialog, "", 10, true)

				if !readActivation(connection) {
					return
				}

				body := map[string]any{"doorbot_id": 1001, "session_id": "s"}
				if wrong == "device" {
					body["doorbot_id"] = 1002
				} else {
					body["session_id"] = "other"
				}

				_ = writeIdentity(connection, dialog, "camera_started", body)
				_, _, _ = connection.ReadMessage()
			})

			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()

			session, err := conn.StartDeviceSession(
				ctx,
				ring.StartDeviceSessionRequest{
					DeviceID: "1001",
					Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
				},
			)
			if session != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cross-session readiness: session=%v err=%v", session != nil, err)
			}
		})
	}
}
func TestMaximumAgeBoundsNegotiationBeforeAnyAnswer(t *testing.T) {
	t.Parallel()

	conn := identityPeer(t, func(c *websocket.Conn, _ string) { _, _, _ = c.ReadMessage() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	session, err := conn.StartDeviceSession(
		ctx,
		ring.StartDeviceSessionRequest{
			DeviceID: "1001",
			Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
			MaxAge:   40 * time.Millisecond,
		},
	)
	if session != nil || !errors.Is(err, ring.ErrSessionExpired) {
		t.Fatalf("max age not enforced during negotiation: %v", err)
	}
}
func TestRemoteCloseTerminatesSessionWithSocketStillOpen(t *testing.T) {
	t.Parallel()

	remoteCloseReady := make(chan struct{})

	var remoteCloseReadyOnce sync.Once

	releaseRemoteClose := func() {
		remoteCloseReadyOnce.Do(func() { close(remoteCloseReady) })
	}

	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		beginIdentity(connection, dialog, "", nil, false) // Missing interval uses documented fallback.

		if !readActivation(connection) {
			return
		}

		_ = writeIdentity(connection, dialog, "camera_started", map[string]any{"doorbot_id": 1001, "session_id": "s"})

		microphone := readSignalRequest(t, connection, "mic_enable")
		if microphone == nil {
			return
		}

		if microphone["dialog_id"] != dialog {
			t.Errorf("microphone dialog = %v, want %s", microphone["dialog_id"], dialog)
		}

		body := replayObjectField(t, microphone, "body")
		if body["doorbot_id"] != float64(1001) || body["session_id"] != "s" || body["enabled"] != false {
			t.Errorf("microphone request differs from expected frame: %v", body)
		}

		<-remoteCloseReady

		_ = writeIdentity(connection, dialog, "close", map[string]any{"doorbot_id": 1001, "session_id": "s"})
		waitForRecordedClientClose(t, connection) // Keep the socket open until Wait resolves.
	})

	t.Cleanup(releaseRemoteClose)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	session, err := conn.StartDeviceSession(
		ctx,
		ring.StartDeviceSessionRequest{
			DeviceID: "1001",
			Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		err = session.SetMicrophone(ctx, ring.SetMicrophoneRequest{})
		if err != nil {
			t.Fatal(err)
		}
	}

	releaseRemoteClose()

	{
		err = session.Wait(ctx)
		if !errors.Is(err, ring.ErrSessionClosed) {
			t.Fatalf("remote close: %v", err)
		}
	}

	if session.State() != ring.SessionClosed {
		t.Fatal("remote close state not published")
	}
}
