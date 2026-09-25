package system_test

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
	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ticket":"synthetic"}`)) }))
	t.Cleanup(tickets.Close)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(4 * time.Second))
		var first struct {
			Method string `json:"method"`
			Dialog string `json:"dialog_id"`
		}
		if err = c.ReadJSON(&first); err != nil {
			return
		}
		if first.Method != "live_view" {
			t.Errorf("unexpected initial method %s", first.Method)
			return
		}
		script(c, first.Dialog)
	}))
	t.Cleanup(peer.Close)
	client, err := ring.NewClient(ring.WithAccessToken("synthetic"), ring.WithHTTPClient(tickets.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}), ring.WithSignalingWebSocketURL("ws"+strings.TrimPrefix(peer.URL, "http")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	t.Cleanup(cancel)
	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}
func writeIdentity(c *websocket.Conn, dialog, method string, body any) error {
	return c.WriteJSON(map[string]any{"method": method, "dialog_id": dialog, "body": body})
}
func beginIdentity(c *websocket.Conn, dialog string, interval any, include bool) {
	_ = writeIdentity(c, dialog, "session_created", map[string]any{"doorbot_id": 1001, "session_id": "s"})
	info := map[string]any{"session_id": "c"}
	if include {
		info["ping_interval"] = interval
	}
	_ = writeIdentity(c, dialog, "sdp", map[string]any{"doorbot_id": 1001, "session_id": "s", "type": "answer", "sdp": answerSDP, "session_info": info})
}
func readActivation(c *websocket.Conn) bool {
	for _, method := range []string{"activate_session", "mic_enable", "stream_options"} {
		var message struct {
			Method string `json:"method"`
		}
		if c.ReadJSON(&message) != nil || message.Method != method {
			return false
		}
	}
	return true
}
func TestNegotiatedHeartbeatRejectsInvalidPresentValues(t *testing.T) {
	for _, value := range []any{0, -1, 61, 1.5, "10", nil} {
		encoded, _ := json.Marshal(value)
		t.Run(string(encoded), func(t *testing.T) {
			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				beginIdentity(c, dialog, value, true)
				_, _, _ = c.ReadMessage()
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}})
			if session != nil || err == nil || !strings.Contains(err.Error(), "heartbeat interval") {
				t.Fatalf("invalid interval accepted: session=%v err=%v", session != nil, err)
			}
		})
	}
}
func TestWrongIdentityCannotDeclareSessionReady(t *testing.T) {
	for _, wrong := range []string{"device", "session"} {
		t.Run(wrong, func(t *testing.T) {
			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				beginIdentity(c, dialog, 10, true)
				if !readActivation(c) {
					return
				}
				body := map[string]any{"doorbot_id": 1001, "session_id": "s"}
				if wrong == "device" {
					body["doorbot_id"] = 1002
				} else {
					body["session_id"] = "other"
				}
				_ = writeIdentity(c, dialog, "camera_started", body)
				_, _, _ = c.ReadMessage()
			})
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}})
			if session != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("cross-session readiness: session=%v err=%v", session != nil, err)
			}
		})
	}
}
func TestMaximumAgeBoundsNegotiationBeforeAnyAnswer(t *testing.T) {
	conn := identityPeer(t, func(c *websocket.Conn, _ string) { _, _, _ = c.ReadMessage() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}, MaxAge: 40 * time.Millisecond})
	if session != nil || !errors.Is(err, ring.ErrSessionExpired) {
		t.Fatalf("max age not enforced during negotiation: %v", err)
	}
}
func TestRemoteCloseTerminatesSessionWithSocketStillOpen(t *testing.T) {
	conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
		beginIdentity(c, dialog, nil, false) // Missing interval uses documented fallback.
		if !readActivation(c) {
			return
		}
		_ = writeIdentity(c, dialog, "camera_started", map[string]any{"doorbot_id": 1001, "session_id": "s"})
		_, _, err := c.ReadMessage()
		if err != nil {
			return
		} // Barrier: caller has the handle.
		_ = writeIdentity(c, dialog, "close", map[string]any{"doorbot_id": 1001, "session_id": "s"})
		_, _, _ = c.ReadMessage() // Socket stays open while Wait must resolve.
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}})
	if err != nil {
		t.Fatal(err)
	}
	if err = session.SetMicrophone(ctx, ring.SetMicrophoneRequest{}); err != nil {
		t.Fatal(err)
	}
	if err = session.Wait(ctx); !errors.Is(err, ring.ErrSessionClosed) {
		t.Fatalf("remote close: %v", err)
	}
	if session.State() != ring.SessionClosed {
		t.Fatal("remote close state not published")
	}
}
