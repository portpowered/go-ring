package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

	tickets := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ticket":"synthetic"}`)) },
		),
	)
	t.Cleanup(tickets.Close)

	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	return conn
}
func writeIdentity(c *websocket.Conn, dialog, method string, body any) error {
	err := c.WriteJSON(map[string]any{"method": method, "dialog_id": dialog, "body": body})
	if err != nil {
		return wrapReplayTestError("write signaling identity frame", err)
	}

	return nil
}
func beginIdentity(connection *websocket.Conn, dialog string, interval any, include bool) {
	_ = writeIdentity(connection, dialog, "session_created", map[string]any{"doorbot_id": 1001, "session_id": "s"})

	info := map[string]any{"session_id": "c"}
	if include {
		info["ping_interval"] = interval
	}

	_ = writeIdentity(
		connection,
		dialog,
		"sdp",
		map[string]any{"doorbot_id": 1001, "session_id": "s", "type": "answer", "sdp": answerSDP, "session_info": info},
	)
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
func TestNegotiatedHeartbeatRejectsInvalidPresentValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value         any
		wantErrorText string
	}{
		{value: 0, wantErrorText: "heartbeat interval"},
		{value: -1, wantErrorText: "heartbeat interval"},
		{value: 61, wantErrorText: "heartbeat interval"},
		{value: 1.5, wantErrorText: "invalid signaling message"},
		{value: "10", wantErrorText: "invalid signaling message"},
		{value: nil, wantErrorText: "heartbeat interval"},
	} {
		value := test.value

		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}

		t.Run(string(encoded), func(t *testing.T) {
			t.Parallel()

			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				beginIdentity(c, dialog, value, true)
				_, _, _ = c.ReadMessage()
			})

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			session, err := conn.StartDeviceSession(
				ctx,
				ring.StartDeviceSessionRequest{
					DeviceID: "1001",
					Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
				},
			)
			if session != nil || err == nil || !strings.Contains(err.Error(), test.wantErrorText) {
				t.Fatalf("invalid interval accepted: session=%v err=%v", session != nil, err)
			}
		})
	}
}
func TestWrongIdentityCannotDeclareSessionReady(t *testing.T) {
	t.Parallel()

	for _, wrong := range []string{"device", "session"} {
		t.Run(wrong, func(t *testing.T) {
			t.Parallel()

			conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
				beginIdentity(connection, dialog, 10, true)

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

	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		beginIdentity(connection, dialog, nil, false) // Missing interval uses documented fallback.

		if !readActivation(connection) {
			return
		}

		_ = writeIdentity(connection, dialog, "camera_started", map[string]any{"doorbot_id": 1001, "session_id": "s"})

		_, _, err := connection.ReadMessage()
		if err != nil {
			return
		} // Barrier: caller has the handle.

		_ = writeIdentity(connection, dialog, "close", map[string]any{"doorbot_id": 1001, "session_id": "s"})
		_, _, _ = connection.ReadMessage() // Socket stays open while Wait must resolve.
	})

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
