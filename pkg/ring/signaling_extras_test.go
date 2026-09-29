package ring

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	dependencywebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
)

func capturedFrame(t *testing.T, method, direction string) signaling.Message {
	t.Helper()

	recording, err := replay.LoadSessionRecording(
		filepath.Join("..", "..", "tests", "replay", "fixtures", "signaling", "historical", "flow-21.json"),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, row := range recording.Messages {
		var message signaling.Message

		err := json.Unmarshal(row.Payload, &message)
		if err != nil {
			t.Fatal(err)
		}

		if message.Method == method && row.Direction == direction {
			return message
		}
	}

	t.Fatalf("captured %s %s not found", direction, method)

	return signaling.Message{}
}
func replayConnection(t *testing.T) (*SignalingConnection, <-chan signaling.Message) {
	t.Helper()

	writes := make(chan signaling.Message, 256)
	ctx, cancel := context.WithCancel(context.Background())
	connection := &SignalingConnection{
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		channels:  make(map[string]chan signaling.Message),
		playbacks: make(map[string]*PlaybackSession),
		sessions:  make(map[string]*DeviceSession),
		pending:   make(map[string]chan signaling.Message),
	}

	connection.writer = dependencywebsocket.NewSignalingWriter(
		connection.done,
		func(_ context.Context, message signaling.Message) error {
			writes <- message

			return nil
		},
		nil,
	)

	go connection.writer.Run()

	t.Cleanup(func() { close(connection.done); cancel(); <-connection.writer.Finished() })

	return connection, writes
}
func replayReply(t *testing.T, connection *SignalingConnection, request signaling.Message, method string) {
	t.Helper()
	message := capturedFrame(t, method, "server_to_client")
	message.DialogID = request.DialogID
	connection.route(message)
}
func TestCapturedPushSubscriptionAndNotification(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	filter := PushFilter{
		FilterIdentifier:  "sanitized-text",
		NotificationScope: "event",
		NotificationType:  "shoulder_tap",
	}
	filter.Filters.DoorbotIDs = []int64{1000}
	result := make(chan *PushSubscription, 1)
	failures := make(chan error, 1)

	go func() {
		subscription, subscribeErr := connection.SubscribePush(context.Background(), []PushFilter{filter})
		result <- subscription

		failures <- subscribeErr
	}()

	request := <-writes
	if request.Method != "push_subscribe" {
		t.Fatalf("method=%s", request.Method)
	}

	var body struct {
		Requested []PushFilter `json:"requested_notifications"`
	}

	if json.Unmarshal(request.Body, &body) != nil || len(body.Requested) != 1 ||
		body.Requested[0].Filters.DoorbotIDs[0] != 1000 {
		t.Fatalf("unexpected subscription body: %s", request.Body)
	}

	replayReply(t, connection, request, "push_subscription_ack")

	subscription := <-result

	{
		err := <-failures
		if err != nil {
			t.Fatal(err)
		}
	}

	replayReply(t, connection, request, "push_event")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	event, err := subscription.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if event.NotificationType != "shoulder_tap" || len(event.Payload) == 0 {
		t.Fatalf("event=%+v", event)
	}

	{
		err := subscription.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	if message := <-writes; message.Method != "push_unsubscribe" {
		t.Fatalf("method=%s", message.Method)
	}
}
func TestCapturedPlaybackNegotiationICEAndTermination(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	requestFrame := capturedFrame(t, "playback", "client_to_server")

	var offer struct {
		SDP string `json:"sdp"`
	}

	{
		err := json.Unmarshal(requestFrame.Body, &offer)
		if err != nil {
			t.Fatal(err)
		}
	}

	result := make(chan *PlaybackSession, 1)
	failures := make(chan error, 1)

	go func() {
		playback, startErr := connection.StartPlayback(
			context.Background(),
			StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: SDPTypeOffer, SDP: offer.SDP}},
		)
		result <- playback

		failures <- startErr
	}()

	request := <-writes
	if request.Method != "playback" {
		t.Fatalf("method=%s", request.Method)
	}

	var sent struct {
		Type  string `json:"type"`
		Entry string `json:"entry_point"`
	}

	_ = json.Unmarshal(request.Body, &sent)

	if sent.Type != "cloud" || sent.Entry != "timeline" {
		t.Fatalf("playback body=%s", request.Body)
	}

	replayReply(t, connection, request, "sdp")

	playback := <-result

	{
		err := <-failures
		if err != nil {
			t.Fatal(err)
		}
	}

	if playback.Answer().Type != SDPTypeAnswer || playback.Answer().SDP == "" {
		t.Fatal("missing playback answer")
	}

	replayReply(t, connection, request, "ice")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	event, err := playback.Receive(ctx)
	if err != nil || event.Method != "ice" {
		t.Fatalf("ICE event=%+v err=%v", event, err)
	}

	replayReply(t, connection, request, "notification")

	event, err = playback.Receive(ctx)
	if err != nil || event.Method != "notification" {
		t.Fatalf("playback notification=%+v err=%v", event, err)
	}

	{
		err := playback.SendICE(ctx, ICECandidateRequest{Candidate: "candidate:synthetic", MLineIndex: 0})
		if err != nil {
			t.Fatal(err)
		}
	}

	if message := <-writes; message.Method != "ice" {
		t.Fatalf("method=%s", message.Method)
	}

	{
		err := playback.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	if message := <-writes; message.Method != "close" {
		t.Fatalf("method=%s", message.Method)
	}
}

func TestPushReplayFailureAndClosure(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	{
		_, err := connection.SubscribePush(context.Background(), nil)
		if err == nil {
			t.Fatal("empty filters accepted")
		}
	}

	filter := PushFilter{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}
	result := make(chan error, 1)

	go func() { _, err := connection.SubscribePush(context.Background(), []PushFilter{filter}); result <- err }()

	request := <-writes
	bad := capturedFrame(t, "push_subscription_ack", "server_to_client")
	bad.DialogID = request.DialogID
	bad.Body = json.RawMessage(`{"status":"denied"}`)
	connection.route(bad)

	err := <-result
	if err == nil {
		t.Fatal("rejected subscription accepted")
	}
}

func TestPlaybackReplayValidation(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)

	for _, req := range []StartPlaybackRequest{
		{DeviceID: "bad"},
		{
			DeviceID: "1000",
			Offer:    SessionDescription{Type: SDPTypeAnswer, SDP: "x"},
		},
		{
			DeviceID: "1000",
			Offer:    SessionDescription{Type: SDPTypeOffer, SDP: "bad"},
		},
	} {
		{
			_, err := connection.StartPlayback(context.Background(), req)
			if err == nil {
				t.Fatalf("accepted %+v", req)
			}
		}
	}

	frame := capturedFrame(t, "playback", "client_to_server")

	var offer struct {
		SDP string `json:"sdp"`
	}

	_ = json.Unmarshal(frame.Body, &offer)
	result := make(chan error, 1)

	go func() {
		_, err := connection.StartPlayback(
			context.Background(),
			StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: SDPTypeOffer, SDP: offer.SDP}},
		)
		result <- err
	}()

	request := <-writes
	bad := capturedFrame(t, "sdp", "server_to_client")
	bad.DialogID = request.DialogID
	bad.Body = json.RawMessage(`{"doorbot_id":999,"session_id":"bad","type":"answer","sdp":"x"}`)
	connection.route(bad)

	err := <-result
	if err == nil {
		t.Fatal("invalid answer accepted")
	}
}

func TestPushReplayIdentityAndReceiveCancellation(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	filters := []PushFilter{{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}}
	ready := make(chan *PushSubscription, 1)

	go func() {
		subscription, _ := connection.SubscribePush(context.Background(), filters)
		ready <- subscription
	}()

	request := <-writes
	replayReply(t, connection, request, "push_subscription_ack")

	subscription := <-ready
	heartbeatDone := make(chan struct{})

	go func() { subscription.heartbeatAt(context.Background(), 100*time.Millisecond); close(heartbeatDone) }()

	select {
	case message := <-writes:
		if message.Method != "push_heartbeat" {
			t.Fatalf("method=%s", message.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("missing push heartbeat")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	{
		_, err := subscription.Receive(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("receive cancellation=%v", err)
		}
	}

	wrong := capturedFrame(t, "push_event", "server_to_client")
	wrong.DialogID = request.DialogID
	wrong.Body = json.RawMessage(
		`{"subscription_id":"other","payload":{},"notification_scope":"event","notification_type":"shoulder_tap"}`,
	)
	connection.route(wrong)

	ctx2, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()

	{
		_, err := subscription.Receive(ctx2)
		if err == nil {
			t.Fatal("foreign event accepted")
		}
	}

	err := subscription.Close()
	if err != nil {
		t.Fatal(err)
	}

	<-heartbeatDone
	<-writes

	{
		_, err := subscription.Receive(context.Background())
		if !errors.Is(err, signaling.ErrClosed) {
			t.Fatalf("closed receive=%v", err)
		}
	}
}

func TestPlaybackReplayReceiveCancellation(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	frame := capturedFrame(t, "playback", "client_to_server")

	var offer struct {
		SDP string `json:"sdp"`
	}

	_ = json.Unmarshal(frame.Body, &offer)
	ready := make(chan *PlaybackSession, 1)

	go func() {
		playback, _ := connection.StartPlayback(
			context.Background(),
			StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: SDPTypeOffer, SDP: offer.SDP}},
		)
		ready <- playback
	}()

	request := <-writes
	replayReply(t, connection, request, "sdp")

	playback := <-ready
	keepaliveDone := make(chan struct{})

	go func() { playback.keepalive(context.Background(), 100*time.Millisecond); close(keepaliveDone) }()

	select {
	case message := <-writes:
		if message.Method != "ping" {
			t.Fatalf("method=%s", message.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("missing playback ping")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	{
		_, err := playback.Receive(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("receive cancellation=%v", err)
		}
	}

	err := playback.Close()
	if err != nil {
		t.Fatal(err)
	}

	<-keepaliveDone
	<-writes

	{
		_, err := playback.Receive(context.Background())
		if !errors.Is(err, signaling.ErrClosed) {
			t.Fatalf("closed receive=%v", err)
		}
	}
}

func TestSignalingExtraValidationBeforeWire(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)

	err := connection.sendTyped(context.Background(), protocol.MethodNotification, "dialog", "", make(chan int))
	if err == nil {
		t.Fatal("unmarshalable body accepted")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	filter := PushFilter{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}
	{
		_, err := connection.SubscribePush(ctx, []PushFilter{filter})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled subscribe=%v", err)
		}
	}

	frame := capturedFrame(t, "playback", "client_to_server")

	var offer struct {
		SDP string `json:"sdp"`
	}

	_ = json.Unmarshal(frame.Body, &offer)

	{
		_, err := connection.StartPlayback(ctx, StartPlaybackRequest{
			DeviceID: "1000",
			Offer:    SessionDescription{Type: SDPTypeOffer, SDP: offer.SDP},
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled playback=%v", err)
		}
	}

	select {
	case message := <-writes:
		t.Fatalf("unexpected write %s", message.Method)
	default:
	}

	connection.mu.Lock()
	connection.closed = true
	connection.terminal = signaling.ErrClosed
	connection.mu.Unlock()

	{
		_, _, err := connection.registerChannel()
		if !errors.Is(err, signaling.ErrClosed) {
			t.Fatalf("closed registration=%v", err)
		}
	}
}

func TestPlaybackCapturedPongAndRemoteClose(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	frame := capturedFrame(t, "playback", "client_to_server")

	var offer struct {
		SDP string `json:"sdp"`
	}

	_ = json.Unmarshal(frame.Body, &offer)
	ready := make(chan *PlaybackSession, 1)

	go func() {
		playback, _ := connection.StartPlayback(
			context.Background(),
			StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: SDPTypeOffer, SDP: offer.SDP}},
		)
		ready <- playback
	}()

	request := <-writes
	replayReply(t, connection, request, "sdp")

	playback := <-ready
	before := playback.lastPong.Load()
	pong := capturedFrame(t, "pong", "server_to_client")
	pong.DialogID = request.DialogID
	pong.Body = json.RawMessage(`{"doorbot_id":999,"session_id":"wrong"}`)
	connection.route(pong)

	if playback.lastPong.Load() != before {
		t.Fatal("foreign pong refreshed playback")
	}

	pong.Body = mustJSON(map[string]any{"doorbot_id": 1000, "session_id": playback.id})
	connection.route(pong)

	if playback.lastPong.Load() < before {
		t.Fatal("matching pong did not refresh playback")
	}

	closeFrame := signaling.Message{Method: "close", DialogID: request.DialogID}
	connection.route(closeFrame)

	{
		_, err := playback.Receive(context.Background())
		if !errors.Is(err, signaling.ErrClosed) {
			t.Fatalf("remote close=%v", err)
		}
	}
}

func TestPlaybackMissingPongTerminates(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	frame := capturedFrame(t, "playback", "client_to_server")

	var offer struct {
		SDP string `json:"sdp"`
	}

	_ = json.Unmarshal(frame.Body, &offer)
	ready := make(chan *PlaybackSession, 1)

	go func() {
		playback, _ := connection.StartPlayback(
			context.Background(),
			StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: SDPTypeOffer, SDP: offer.SDP}},
		)
		ready <- playback
	}()

	request := <-writes
	replayReply(t, connection, request, "sdp")

	playback := <-ready
	playback.lastPong.Store(time.Now().Add(-time.Minute).UnixNano())

	finished := make(chan struct{})

	go func() { playback.keepalive(context.Background(), time.Millisecond); close(finished) }()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("missing pong did not terminate")
	}

	{
		_, err := playback.Receive(context.Background())
		if !errors.Is(err, signaling.ErrHeartbeat) {
			t.Fatalf("timeout receive=%v", err)
		}
	}
}

func TestPushBackpressureIsolatesDialog(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	ready := make(chan *PushSubscription, 1)

	go func() {
		subscription, _ := connection.SubscribePush(
			context.Background(),
			[]PushFilter{{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}},
		)
		ready <- subscription
	}()

	request := <-writes
	replayReply(t, connection, request, "push_subscription_ack")

	push := <-ready
	event := capturedFrame(t, "push_event", "server_to_client")

	event.DialogID = request.DialogID

	for range cap(push.events) {
		connection.route(event)
	}

	connection.route(event)

	{
		_, err := push.Receive(context.Background())
		if !errors.Is(err, signaling.ErrBackpressure) {
			t.Fatalf("full push queue error = %v", err)
		}
	}

	select {
	case <-connection.done:
		t.Fatal("push backpressure closed shared signaling connection")
	default:
	}

	other := make(chan signaling.Message, 1)

	connection.mu.Lock()
	connection.channels["unrelated"] = other
	connection.mu.Unlock()
	connection.route(signaling.Message{Method: "notification", DialogID: "unrelated"})

	select {
	case <-other:
	default:
		t.Fatal("other dialog stopped receiving after push backpressure")
	}
}

func TestSessionKeepalivesStopWithOwnerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	connection := &SignalingConnection{done: make(chan struct{})}

	for _, test := range []struct {
		name string
		run  func()
	}{
		{"push", func() {
			(&PushSubscription{connection: connection, done: make(chan struct{})}).heartbeatAt(ctx, time.Hour)
		}},
		{"playback", func() {
			(&PlaybackSession{connection: connection, done: make(chan struct{})}).keepalive(ctx, time.Hour)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			completed := make(chan struct{})

			go func() { test.run(); close(completed) }()

			select {
			case <-completed:
			case <-time.After(time.Second):
				t.Fatal("keepalive ignored owner cancellation")
			}
		})
	}
}

func TestPlaybackLifetimeExpiresAndSendsClose(t *testing.T) {
	t.Parallel()

	connection, writes := replayConnection(t)
	session := &PlaybackSession{
		connection: connection, dialog: "lifetime", riid: "request", id: "session",
		deviceID: 1000, done: make(chan struct{}),
	}
	finished := make(chan struct{})

	go func() {
		session.keepaliveFor(context.Background(), time.Hour, time.Millisecond)
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("playback lifetime did not expire")
	}

	select {
	case message := <-writes:
		if message.Method != protocol.MethodClose {
			t.Fatalf("expired playback sent %s, want close", message.Method)
		}
	default:
		t.Fatal("expired playback did not send close")
	}

	if !errors.Is(session.terminal, signaling.ErrClosed) {
		t.Fatalf("terminal error = %v, want closed", session.terminal)
	}
}
