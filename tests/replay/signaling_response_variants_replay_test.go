package replay_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
)

type signalingResponseCase struct {
	Case  string `json:"case"`
	Flow  string `json:"flow"`
	Field string `json:"field"`
}

func TestRecordedSignalingResponseVariants(t *testing.T) {
	cases, err := replay.LoadCases[signalingResponseCase](filepath.Join("fixtures", "porting", "signaling-response-variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			conn := openRecordedPeer(t, func(c *websocket.Conn) {
				method, dialog := "push_subscribe", "dialog-1"
				if tc.Flow == "playback" {
					method, dialog = "playback", "dialog-2"
				}
				request := readSignalRequest(t, c, method)
				if request == nil {
					return
				}
				response := "push_subscription_ack"
				if tc.Flow == "playback" {
					response = "sdp"
				}
				body := capturedSignalFrame(t, "server_to_client", dialog, response)
				switch tc.Field {
				case "subscription_id", "session_id", "sdp":
					body[tc.Field] = ""
				case "status":
					body[tc.Field] = "rejected"
				case "type":
					body[tc.Field] = "offer"
				}
				var payload any = body
				if tc.Field == "malformed" {
					payload = "bad-body"
				}
				_ = c.WriteJSON(map[string]any{"method": response, "dialog_id": request["dialog_id"], "body": payload})
				_, _, _ = c.ReadMessage()
			})
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if tc.Flow == "push" {
				_, err = conn.SubscribePush(ctx, []ring.PushFilter{{FilterIdentifier: "fixture", NotificationScope: "event", NotificationType: "shoulder_tap"}})
			} else {
				_, err = conn.StartPlayback(ctx, ring.StartPlaybackRequest{DeviceID: "1000", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: recordedPlaybackOffer(t)}})
			}
			if err == nil {
				t.Fatal("invalid captured response accepted")
			}
		})
	}
}
