package replay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

func capturedSignalFrame(t *testing.T, direction, dialog, method string) map[string]any {
	t.Helper()
	for _, row := range loadConversation(t, "flow-21.json").Messages {
		if row.Direction != direction || row.Payload.DialogID != dialog || row.Payload.Method != method {
			continue
		}
		var frame map[string]any
		if err := json.Unmarshal(row.Payload.Body, &frame); err != nil {
			t.Fatal(err)
		}
		return frame
	}
	t.Fatalf("missing captured %s %s on %s", direction, method, dialog)
	return nil
}

func readSignalRequest(t *testing.T, c *websocket.Conn, method string) map[string]any {
	t.Helper()
	var frame map[string]any
	if err := c.ReadJSON(&frame); err != nil {
		t.Error(err)
		return nil
	}
	if frame["method"] != method {
		t.Errorf("signal method = %v, want %s", frame["method"], method)
		return nil
	}
	return frame
}

func writeCapturedSignal(t *testing.T, c *websocket.Conn, capturedDialog string, request map[string]any, method string) {
	t.Helper()
	frame := map[string]any{
		"method": method, "dialog_id": request["dialog_id"],
		"body": capturedSignalFrame(t, "server_to_client", capturedDialog, method),
	}
	if err := c.WriteJSON(frame); err != nil {
		t.Error(err)
	}
}

func TestRecordedPublicPushSubscriptionAndEvent(t *testing.T) {
	conn := openRecordedPeer(t, func(c *websocket.Conn) {
		request := readSignalRequest(t, c, "push_subscribe")
		if request == nil {
			return
		}
		body := request["body"].(map[string]any)
		filters := body["requested_notifications"].([]any)
		if len(filters) != 1 || filters[0].(map[string]any)["notification_type"] != "shoulder_tap" {
			t.Errorf("push filters differ from capture: %v", filters)
		}
		writeCapturedSignal(t, c, "dialog-1", request, "push_subscription_ack")
		writeCapturedSignal(t, c, "dialog-1", request, "push_event")
		_ = readSignalRequest(t, c, "push_unsubscribe")
	})
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	filter := ring.PushFilter{FilterIdentifier: "sanitized-text", NotificationScope: "event", NotificationType: "shoulder_tap", Filters: ring.PushFilters{DoorbotIDs: []int64{1000}}}
	subscription, err := conn.SubscribePush(ctx, []ring.PushFilter{filter})
	if err != nil {
		t.Fatal(err)
	}
	event, err := subscription.Receive(ctx)
	if err != nil || event.NotificationType != "shoulder_tap" || len(event.Payload) == 0 {
		t.Fatalf("recorded push event = %+v, %v", event, err)
	}
	if err := subscription.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedPublicPushRejection(t *testing.T) {
	conn := openRecordedPeer(t, func(c *websocket.Conn) {
		request := readSignalRequest(t, c, "push_subscribe")
		if request == nil {
			return
		}
		body := capturedSignalFrame(t, "server_to_client", "dialog-1", "push_subscription_ack")
		body["status"] = "denied"
		if err := c.WriteJSON(map[string]any{"method": "push_subscription_ack", "dialog_id": request["dialog_id"], "body": body}); err != nil {
			t.Error(err)
		}
		_, _, _ = c.ReadMessage()
	})
	defer conn.Close()
	if _, err := conn.SubscribePush(context.Background(), nil); err == nil {
		t.Fatal("empty push filters accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := conn.SubscribePush(ctx, []ring.PushFilter{{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}}); err == nil {
		t.Fatal("rejected subscription accepted")
	}
}

func recordedPlaybackOffer(t *testing.T) string {
	t.Helper()
	return capturedSignalFrame(t, "client_to_server", "dialog-2", "playback")["sdp"].(string)
}

func TestRecordedPublicPlaybackAnswerICEAndClose(t *testing.T) {
	conn := openRecordedPeer(t, func(c *websocket.Conn) {
		request := readSignalRequest(t, c, "playback")
		if request == nil {
			return
		}
		body := request["body"].(map[string]any)
		if body["type"] != "cloud" || body["entry_point"] != "timeline" || body["sdp"] != recordedPlaybackOffer(t) {
			t.Errorf("playback offer differs from capture")
		}
		writeCapturedSignal(t, c, "dialog-2", request, "sdp")
		writeCapturedSignal(t, c, "dialog-2", request, "ice")
		writeCapturedSignal(t, c, "dialog-2", request, "notification")
		_ = readSignalRequest(t, c, "ice")
		_ = readSignalRequest(t, c, "close")
	})
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	session, err := conn.StartPlayback(ctx, ring.StartPlaybackRequest{DeviceID: "1000", Offer: ring.SessionDescription{Type: "offer", SDP: recordedPlaybackOffer(t)}})
	if err != nil {
		t.Fatal(err)
	}
	if session.Answer().Type != "answer" || session.Answer().SDP == "" {
		t.Fatal("captured playback answer missing")
	}
	for _, method := range []string{"ice", "notification"} {
		event, err := session.Receive(ctx)
		if err != nil || event.Method != method {
			t.Fatalf("playback event = %+v, %v; want %s", event, err, method)
		}
	}
	if err := session.SendICE(ctx, ring.ICECandidateRequest{Candidate: "candidate:synthetic", MLineIndex: 0}); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedPublicPlaybackRejectsInvalidAnswer(t *testing.T) {
	conn := openRecordedPeer(t, func(c *websocket.Conn) {
		request := readSignalRequest(t, c, "playback")
		if request == nil {
			return
		}
		body := capturedSignalFrame(t, "server_to_client", "dialog-2", "sdp")
		body["doorbot_id"] = float64(999)
		if err := c.WriteJSON(map[string]any{"method": "sdp", "dialog_id": request["dialog_id"], "body": body}); err != nil {
			t.Error(err)
		}
		_, _, _ = c.ReadMessage()
	})
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := conn.StartPlayback(ctx, ring.StartPlaybackRequest{DeviceID: "1000", Offer: ring.SessionDescription{Type: "offer", SDP: recordedPlaybackOffer(t)}}); err == nil {
		t.Fatal("invalid playback answer accepted")
	}
}
