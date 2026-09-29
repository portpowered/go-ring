package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/ring"
)

const (
	canceledNegotiationScenario = "canceled negotiation"
	wrongSubscriptionScenario   = "wrong subscription"
)

func capturedSignalFrame(t *testing.T, direction, dialog, method string) map[string]any {
	t.Helper()

	for _, row := range loadConversation(t, "flow-21.json").Messages {
		if row.Direction != direction || row.Payload.DialogID != dialog || row.Payload.Method != method {
			continue
		}

		var frame map[string]any

		err := json.Unmarshal(row.Payload.Body, &frame)
		if err != nil {
			t.Fatal(err)
		}

		return frame
	}

	t.Fatalf("missing captured %s %s on %s", direction, method, dialog)

	return nil
}

func readSignalRequest(t *testing.T, connection *websocket.Conn, method string) map[string]any {
	t.Helper()

	var frame map[string]any

	err := connection.ReadJSON(&frame)
	if err != nil {
		t.Error(err)

		return nil
	}

	if frame["method"] != method {
		t.Errorf("signal method = %v, want %s", frame["method"], method)

		return nil
	}

	return frame
}

func writeCapturedSignal(
	t *testing.T,
	connection *websocket.Conn,
	capturedDialog string,
	request map[string]any,
	method string,
) {
	t.Helper()

	frame := map[string]any{
		"method": method, "dialog_id": request["dialog_id"],
		"body": capturedSignalFrame(t, "server_to_client", capturedDialog, method),
	}

	err := connection.WriteJSON(frame)
	if err != nil {
		t.Error(err)
	}
}

func TestRecordedPublicPushSubscriptionAndEvent(t *testing.T) {
	t.Parallel()

	conn := openRecordedPeer(t, func(connection *websocket.Conn) {
		request := readSignalRequest(t, connection, "push_subscribe")
		if request == nil {
			return
		}

		body := replayObjectField(t, request, "body")

		filters := replayArrayField(t, body, "requested_notifications")
		if len(filters) != 1 {
			t.Errorf("push filters differ from capture: %v", filters)

			return
		}

		filter := replayObjectValue(t, filters[0], "requested_notifications[0]")
		if filter["notification_type"] != shoulderTapNotificationType {
			t.Errorf("push filters differ from capture: %v", filters)
		}

		writeCapturedSignal(t, connection, "dialog-1", request, "push_subscription_ack")
		writeCapturedSignal(t, connection, "dialog-1", request, "push_event")

		if readSignalRequest(t, connection, "push_unsubscribe") != nil {
			waitForRecordedClientClose(t, connection)
		}
	})

	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	filter := ring.PushFilter{
		FilterIdentifier:  "sanitized-text",
		NotificationScope: "event",
		NotificationType:  shoulderTapNotificationType,
		Filters:           ring.PushFilters{DoorbotIDs: []int64{1000}},
	}

	subscription, err := conn.SubscribePush(ctx, []ring.PushFilter{filter})
	if err != nil {
		t.Fatal(err)
	}

	event, err := subscription.Receive(ctx)
	if err != nil || event.NotificationType != shoulderTapNotificationType || len(event.Payload) == 0 {
		t.Fatalf("recorded push event = %+v, %v", event, err)
	}

	{
		err := subscription.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRecordedPublicPushRejection(t *testing.T) {
	t.Parallel()

	conn := openRecordedPeer(t, func(connection *websocket.Conn) {
		request := readSignalRequest(t, connection, "push_subscribe")
		if request == nil {
			return
		}

		body := capturedSignalFrame(t, "server_to_client", "dialog-1", "push_subscription_ack")

		body["status"] = "denied"

		err := connection.WriteJSON(
			map[string]any{"method": "push_subscription_ack", "dialog_id": request["dialog_id"], "body": body},
		)
		if err != nil {
			t.Error(err)
		}

		_, _, _ = connection.ReadMessage()
	})

	defer func() { _ = conn.Close() }()

	{
		_, err := conn.SubscribePush(context.Background(), nil)
		if err == nil {
			t.Fatal("empty push filters accepted")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	{
		_, err := conn.SubscribePush(ctx, []ring.PushFilter{
			{
				FilterIdentifier:  "one",
				NotificationScope: "event",
				NotificationType:  shoulderTapNotificationType,
			},
		})
		if err == nil {
			t.Fatal("rejected subscription accepted")
		}
	}
}

func recordedPlaybackOffer(t *testing.T) string {
	t.Helper()

	return replayStringField(t, capturedSignalFrame(t, "client_to_server", "dialog-2", "playback"), "sdp")
}

func TestRecordedPublicPlaybackAnswerICEAndClose(t *testing.T) {
	t.Parallel()

	closeReceived := make(chan struct{})
	allowPeerClose := make(chan struct{})

	t.Cleanup(func() { close(allowPeerClose) })
	conn := openRecordedPeer(t, func(connection *websocket.Conn) {
		request := readSignalRequest(t, connection, "playback")
		if request == nil {
			return
		}

		body := replayObjectField(t, request, "body")
		if body["type"] != "cloud" || body["entry_point"] != "timeline" || body["sdp"] != recordedPlaybackOffer(t) {
			t.Errorf("playback offer differs from capture")
		}

		writeCapturedSignal(t, connection, "dialog-2", request, "sdp")
		writeCapturedSignal(t, connection, "dialog-2", request, "ice")
		writeCapturedSignal(t, connection, "dialog-2", request, "notification")

		_ = readSignalRequest(t, connection, "ice")

		if readSignalRequest(t, connection, "close") != nil {
			close(closeReceived)
		}

		<-allowPeerClose
	})

	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	session, err := conn.StartPlayback(
		ctx,
		ring.StartPlaybackRequest{
			DeviceID: "1000",
			Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: recordedPlaybackOffer(t)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	if session.Answer().Type != ring.SDPTypeAnswer || session.Answer().SDP == "" {
		t.Fatal("captured playback answer missing")
	}

	for _, method := range []string{"ice", "notification"} {
		event, err := session.Receive(ctx)
		if err != nil || event.Method != method {
			t.Fatalf("playback event = %+v, %v; want %s", event, err, method)
		}
	}

	{
		err := session.SendICE(ctx, ring.ICECandidateRequest{Candidate: "candidate:synthetic", MLineIndex: 0})
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := session.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	select {
	case <-closeReceived:
	case <-time.After(time.Second):
		t.Fatal("peer did not receive playback close")
	}
}

func TestRecordedPublicPlaybackRejectsInvalidAnswer(t *testing.T) {
	t.Parallel()

	conn := openRecordedPeer(t, func(connection *websocket.Conn) {
		request := readSignalRequest(t, connection, "playback")
		if request == nil {
			return
		}

		body := capturedSignalFrame(t, "server_to_client", "dialog-2", "sdp")

		body["doorbot_id"] = float64(999)

		err := connection.WriteJSON(map[string]any{"method": "sdp", "dialog_id": request["dialog_id"], "body": body})
		if err != nil {
			t.Error(err)
		}

		_, _, _ = connection.ReadMessage()
	})

	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	{
		_, err := conn.StartPlayback(ctx, ring.StartPlaybackRequest{
			DeviceID: "1000",
			Offer: ring.SessionDescription{
				Type: ring.SDPTypeOffer,
				SDP:  recordedPlaybackOffer(t),
			},
		})
		if err == nil {
			t.Fatal("invalid playback answer accepted")
		}
	}
}

func TestRecordedPublicPushIdentityAndCancellation(t *testing.T) {
	t.Parallel()

	for _, scenario := range []string{wrongSubscriptionScenario, canceledNegotiationScenario, "closed subscription"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			runRecordedPublicPushIdentityScenario(t, scenario)
		})
	}
}

func runRecordedPublicPushIdentityScenario(t *testing.T, scenario string) {
	t.Helper()

	conn := openRecordedPeer(t, func(connection *websocket.Conn) {
		serveRecordedPushIdentityScenario(t, connection, scenario)
	})

	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	filter := ring.PushFilter{
		FilterIdentifier:  "sanitized-text",
		NotificationScope: "event",
		NotificationType:  shoulderTapNotificationType,
	}

	subscription, err := conn.SubscribePush(ctx, []ring.PushFilter{filter})
	if scenario == canceledNegotiationScenario {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("canceled subscription = %v", err)
		}

		return
	}

	if err != nil {
		t.Fatal(err)
	}

	if scenario == wrongSubscriptionScenario {
		assertForeignPushEventRejected(t, subscription)
	}

	assertPublicPushSubscriptionCloses(t, subscription)
}

func serveRecordedPushIdentityScenario(t *testing.T, connection *websocket.Conn, scenario string) {
	t.Helper()

	request := readSignalRequest(t, connection, "push_subscribe")
	if request == nil {
		return
	}

	if scenario == canceledNegotiationScenario {
		_, _, _ = connection.ReadMessage()

		return
	}

	writeCapturedSignal(t, connection, "dialog-1", request, "push_subscription_ack")

	if scenario == wrongSubscriptionScenario {
		body := capturedSignalFrame(t, "server_to_client", "dialog-1", "push_event")
		body["subscription_id"] = "foreign-subscription"
		_ = connection.WriteJSON(map[string]any{
			"method": "push_event", "dialog_id": request["dialog_id"], "body": body,
		})
	}

	if readSignalRequest(t, connection, "push_unsubscribe") != nil {
		waitForRecordedClientClose(t, connection)
	}
}

func assertForeignPushEventRejected(t *testing.T, subscription *ring.PushSubscription) {
	t.Helper()

	receiveCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()

	_, err := subscription.Receive(receiveCtx)
	if err == nil {
		t.Fatal("foreign push event accepted")
	}
}

func assertPublicPushSubscriptionCloses(t *testing.T, subscription *ring.PushSubscription) {
	t.Helper()

	err := subscription.Close()
	if err != nil {
		t.Fatal(err)
	}

	_, err = subscription.Receive(context.Background())
	if !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("closed subscription receive = %v", err)
	}
}

func TestRecordedPublicPlaybackRemoteCloseAndValidation(t *testing.T) {
	t.Parallel()

	conn := openRecordedPeer(t, func(connection *websocket.Conn) {
		request := readSignalRequest(t, connection, "playback")
		if request == nil {
			return
		}

		writeCapturedSignal(t, connection, "dialog-2", request, "sdp")

		if readSignalRequest(t, connection, "ice") == nil {
			return
		}

		pong := capturedSignalFrame(t, "server_to_client", "dialog-2", "pong")
		_ = connection.WriteJSON(map[string]any{"method": "pong", "dialog_id": request["dialog_id"], "body": pong})
		_ = connection.WriteJSON(
			map[string]any{
				"method":    "close",
				"dialog_id": request["dialog_id"],
				"body":      capturedSignalFrame(t, "client_to_server", "dialog-2", "close"),
			},
		)
		_, _, _ = connection.ReadMessage()
	})

	defer func() { _ = conn.Close() }()

	for _, invalid := range []ring.StartPlaybackRequest{
		{DeviceID: "invalid"},
		{DeviceID: "1000", Offer: ring.SessionDescription{Type: ring.SDPTypeAnswer, SDP: recordedPlaybackOffer(t)}},
		{DeviceID: "1000", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: "bad"}},
	} {
		{
			_, err := conn.StartPlayback(context.Background(), invalid)
			if err == nil {
				t.Fatalf("invalid playback request accepted: %+v", invalid)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	session, err := conn.StartPlayback(
		ctx,
		ring.StartPlaybackRequest{
			DeviceID: "1000",
			Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: recordedPlaybackOffer(t)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		err := session.SendICE(ctx, ring.ICECandidateRequest{Candidate: "candidate:synthetic", MLineIndex: 0})
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		_, err := session.Receive(ctx)
		if !errors.Is(err, signaling.ErrClosed) {
			t.Fatalf("remote playback close = %v", err)
		}
	}
}

func TestRecordedPublicPlaybackPingPongLifecycle(t *testing.T) {
	t.Parallel()

	conn := openRecordedPeer(t, func(connection *websocket.Conn) {
		request := readSignalRequest(t, connection, "playback")
		if request == nil {
			return
		}

		answer := capturedSignalFrame(t, "server_to_client", "dialog-2", "sdp")

		replayObjectField(t, answer, "session_info")["ping_interval"] = float64(1)

		err := connection.WriteJSON(map[string]any{"method": "sdp", "dialog_id": request["dialog_id"], "body": answer})
		if err != nil {
			t.Error(err)

			return
		}

		pong := capturedSignalFrame(t, "server_to_client", "dialog-2", "pong")

		for range 2 {
			ping := readSignalRequest(t, connection, "ping")
			if ping == nil || replayStringField(t, replayObjectField(t, ping, "body"), "session_id") != answer["session_id"] {
				t.Errorf("playback ping identity differs from captured session")

				return
			}

			err := connection.WriteJSON(map[string]any{"method": "pong", "dialog_id": request["dialog_id"], "body": pong})
			if err != nil {
				t.Error(err)

				return
			}
		}

		_ = connection.WriteJSON(
			map[string]any{
				"method":    "close",
				"dialog_id": request["dialog_id"],
				"body":      capturedSignalFrame(t, "client_to_server", "dialog-2", "close"),
			},
		)
		_, _, _ = connection.ReadMessage()
	})

	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	session, err := conn.StartPlayback(
		ctx,
		ring.StartPlaybackRequest{
			DeviceID: "1000",
			Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: recordedPlaybackOffer(t)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		_, err := session.Receive(ctx)
		if !errors.Is(err, signaling.ErrClosed) {
			t.Fatalf("playback did not terminate after recorded pong cycle: %v", err)
		}
	}
}
