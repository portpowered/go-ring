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

func TestStartSessionRejectsInvalidOfferBeforeOpeningSession(t *testing.T) {
	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ticket":"x"}`))
	}))
	defer tickets.Close()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := up.Upgrade(w, r, nil)
		if e == nil {
			defer c.Close()
			_, _, _ = c.ReadMessage()
		}
	}))
	defer ws.Close()
	url := "ws" + strings.TrimPrefix(ws.URL, "http")
	client, err := ring.NewClient(ring.WithAccessToken("x"), ring.WithHTTPClient(tickets.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}), ring.WithSignalingWebSocketURL(url))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, req := range []ring.StartDeviceSessionRequest{
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: "answer", SDP: offerSDP}},
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: "not SDP"}},
		{DeviceID: "bad", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}},
	} {
		if _, err := conn.StartDeviceSession(context.Background(), req); err == nil {
			t.Fatalf("accepted invalid request: %+v", req)
		}
	}
}

func TestOversizedTicketResponseIsRejected(t *testing.T) {
	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	}))
	defer tickets.Close()
	client, err := ring.NewClient(ring.WithAccessToken("x"), ring.WithHTTPClient(tickets.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}), ring.WithSignalingWebSocketURL("ws://127.0.0.1/unused"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{}); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized ticket response error = %v", err)
	}
}

func TestNegotiationCancellationAndPendingRPCFailure(t *testing.T) {
	for _, mode := range []string{"negotiation_cancel", "rpc_remote_close", "session_expiry", "heartbeat_timeout", "event_backpressure", "malformed_rpc_envelope", "malformed_close"} {
		t.Run(mode, func(t *testing.T) {
			tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ticket":"x"}`)) }))
			defer tickets.Close()
			up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
			ready := make(chan struct{})
			peerDone := make(chan struct{})
			ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := up.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				defer close(peerDone)
				_, b, err := c.ReadMessage()
				if err != nil {
					return
				}
				var first struct {
					Method string `json:"method"`
					Dialog string `json:"dialog_id"`
				}
				if jsonErr := json.Unmarshal(b, &first); jsonErr != nil || first.Method != "live_view" {
					return
				}
				close(ready)
				if mode == "negotiation_cancel" {
					_, _, _ = c.ReadMessage()
					return
				}
				write := func(v any) error {
					raw, e := json.Marshal(v)
					if e == nil {
						e = c.WriteMessage(websocket.TextMessage, raw)
					}
					return e
				}
				_ = write(map[string]any{"method": "session_created", "dialog_id": first.Dialog, "riid": "r", "body": map[string]any{"doorbot_id": 1001, "session_id": "s"}})
				pingInterval := 10
				if mode == "heartbeat_timeout" {
					pingInterval = 1
				}
				_ = write(map[string]any{"method": "sdp", "dialog_id": first.Dialog, "riid": "r", "body": map[string]any{"doorbot_id": 1001, "session_id": "s", "type": "answer", "sdp": answerSDP, "session_info": map[string]any{"session_id": "c", "ping_interval": pingInterval}}})
				for _, want := range []string{"activate_session", "mic_enable", "stream_options"} {
					_, msg, e := c.ReadMessage()
					if e != nil || !strings.Contains(string(msg), want) {
						return
					}
				}
				_ = write(map[string]any{"method": "camera_started", "dialog_id": first.Dialog, "riid": "r", "body": map[string]any{"doorbot_id": 1001, "session_id": "s"}})
				if mode == "malformed_rpc_envelope" {
					_, _, _ = c.ReadMessage() // Wait until StartDeviceSession has returned.
					_ = write(map[string]any{"method": "rpc", "dialog_id": first.Dialog, "riid": "r", "body": map[string]any{"doorbot_id": 1001, "session_id": "s", "command": "broken"}})
					return
				}
				if mode == "malformed_close" {
					_, _, _ = c.ReadMessage() // Wait until StartDeviceSession has returned.
					_ = write(map[string]any{"method": "close", "dialog_id": first.Dialog, "riid": "r", "body": "malformed"})
					return
				}
				if mode == "heartbeat_timeout" {
					pings := 0
					for i := 0; i < 4; i++ {
						_, msg, e := c.ReadMessage()
						if e != nil {
							return
						}
						var v struct {
							Method string `json:"method"`
						}
						_ = json.Unmarshal(msg, &v)
						if v.Method == "ping" {
							pings++
						}
						if v.Method == "close" {
							if pings < 2 {
								t.Errorf("heartbeat closed after only %d pings", pings)
							}
							return
						}
					}
					return
				}
				if mode == "event_backpressure" {
					_, _, _ = c.ReadMessage() // Wait until StartDeviceSession has returned.
					for i := 0; i < 40; i++ {
						if write(map[string]any{"method": "motion_event", "dialog_id": first.Dialog, "riid": "r", "body": map[string]any{"doorbot_id": 1001, "session_id": "s", "sequence": i}}) != nil {
							return
						}
					}
					return
				}
				_, msg, e := c.ReadMessage()
				if e != nil {
					return
				}
				if mode == "session_expiry" && !strings.Contains(string(msg), `"method":"close"`) {
					t.Errorf("expiry did not send close: %s", msg)
				}
				// For the RPC case, closing after reading its request must fail its waiter.
			}))
			defer ws.Close()
			url := "ws" + strings.TrimPrefix(ws.URL, "http")
			client, err := ring.NewClient(ring.WithAccessToken("x"), ring.WithHTTPClient(tickets.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: tickets.URL}), ring.WithSignalingWebSocketURL(url))
			if err != nil {
				t.Fatal(err)
			}
			conn, err := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if mode == "negotiation_cancel" {
				ctx, cancel := context.WithCancel(context.Background())
				result := make(chan error, 1)
				go func() {
					_, e := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}})
					result <- e
				}()
				select {
				case <-ready:
				case <-time.After(time.Second):
					t.Fatal("live_view was not sent")
				}
				cancel()
				select {
				case e := <-result:
					if e == nil || !strings.Contains(e.Error(), "canceled") {
						t.Fatalf("negotiation result = %v", e)
					}
				case <-time.After(time.Second):
					t.Fatal("negotiation did not cancel")
				}
				return
			}
			request := ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: "offer", SDP: offerSDP}}
			if mode == "session_expiry" || mode == "heartbeat_timeout" || mode == "event_backpressure" || mode == "malformed_rpc_envelope" || mode == "malformed_close" {
				if mode == "session_expiry" {
					request.MaxAge = 25 * time.Millisecond
				}
			}
			session, err := conn.StartDeviceSession(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "event_backpressure" {
				if err = session.SetMicrophone(context.Background(), ring.SetMicrophoneRequest{Enabled: true}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "session_expiry" || mode == "heartbeat_timeout" || mode == "event_backpressure" {
				waitLimit := time.Second
				want := error(ring.ErrSessionExpired)
				state := ring.SessionExpired
				if mode == "heartbeat_timeout" {
					waitLimit = 5 * time.Second
					want = ring.ErrSessionHeartbeat
					state = ring.SessionFailed
				}
				if mode == "event_backpressure" {
					want = ring.ErrSessionBackpressure
					state = ring.SessionFailed
				}
				ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
				err = session.Wait(ctx)
				cancel()
				if !errors.Is(err, want) || session.State() != state {
					t.Fatalf("expired session state=%s err=%v", session.State(), err)
				}
				return
			}
			if mode == "malformed_rpc_envelope" || mode == "malformed_close" {
				if err = session.SetMicrophone(context.Background(), ring.SetMicrophoneRequest{Enabled: true}); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				err = session.Wait(ctx)
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
		})
	}
}
