package ring

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/internal/testkit/replay"
)

func capturedFrame(t *testing.T, method, direction string) signaling.Message {
	t.Helper()
	r, err := replay.LoadSessionRecording(filepath.Join("..", "..", "test", "recordings", "sessions", "flow-21.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range r.Messages {
		var m signaling.Message
		if err := json.Unmarshal(row.Payload, &m); err != nil {
			t.Fatal(err)
		}
		if m.Method == method && row.Direction == direction {
			return m
		}
	}
	t.Fatalf("captured %s %s not found", direction, method)
	return signaling.Message{}
}
func replayConnection(t *testing.T) (*SignalingConnection, <-chan signaling.Message) {
	t.Helper()
	writes := make(chan signaling.Message, 256)
	ctx, cancel := context.WithCancel(context.Background())
	c := &SignalingConnection{ctx: ctx, cancel: cancel, done: make(chan struct{}), channels: make(map[string]chan signaling.Message), playbacks: make(map[string]*PlaybackSession), sessions: make(map[string]*DeviceSession), pending: make(map[string]chan signaling.Message)}
	c.writer = newSignalingWriter(c.done, func(_ context.Context, m signaling.Message) error { writes <- m; return nil }, nil)
	go c.writer.run()
	t.Cleanup(func() { close(c.done); cancel(); <-c.writer.finished })
	return c, writes
}
func replayReply(t *testing.T, c *SignalingConnection, request signaling.Message, method string) {
	t.Helper()
	m := capturedFrame(t, method, "server_to_client")
	m.DialogID = request.DialogID
	c.route(m)
}
func TestCapturedPushSubscriptionAndNotification(t *testing.T) {
	c, writes := replayConnection(t)
	filter := PushFilter{FilterIdentifier: "sanitized-text", NotificationScope: "event", NotificationType: "shoulder_tap"}
	filter.Filters.DoorbotIDs = []int64{1000}
	result := make(chan *PushSubscription, 1)
	failures := make(chan error, 1)
	go func() {
		s, e := c.SubscribePush(context.Background(), []PushFilter{filter})
		result <- s
		failures <- e
	}()
	request := <-writes
	if request.Method != "push_subscribe" {
		t.Fatalf("method=%s", request.Method)
	}
	var body struct {
		Requested []PushFilter `json:"requested_notifications"`
	}
	if json.Unmarshal(request.Body, &body) != nil || len(body.Requested) != 1 || body.Requested[0].Filters.DoorbotIDs[0] != 1000 {
		t.Fatalf("unexpected subscription body: %s", request.Body)
	}
	replayReply(t, c, request, "push_subscription_ack")
	s := <-result
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	replayReply(t, c, request, "push_event")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := s.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if event.NotificationType != "shoulder_tap" || len(event.Payload) == 0 {
		t.Fatalf("event=%+v", event)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if m := <-writes; m.Method != "push_unsubscribe" {
		t.Fatalf("method=%s", m.Method)
	}
}
func TestCapturedPlaybackNegotiationICEAndTermination(t *testing.T) {
	c, writes := replayConnection(t)
	requestFrame := capturedFrame(t, "playback", "client_to_server")
	var offer struct {
		SDP string `json:"sdp"`
	}
	if err := json.Unmarshal(requestFrame.Body, &offer); err != nil {
		t.Fatal(err)
	}
	result := make(chan *PlaybackSession, 1)
	failures := make(chan error, 1)
	go func() {
		s, e := c.StartPlayback(context.Background(), StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: "offer", SDP: offer.SDP}})
		result <- s
		failures <- e
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
	replayReply(t, c, request, "sdp")
	s := <-result
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if s.Answer().Type != "answer" || s.Answer().SDP == "" {
		t.Fatal("missing playback answer")
	}
	replayReply(t, c, request, "ice")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	event, err := s.Receive(ctx)
	if err != nil || event.Method != "ice" {
		t.Fatalf("ICE event=%+v err=%v", event, err)
	}
	replayReply(t, c, request, "notification")
	event, err = s.Receive(ctx)
	if err != nil || event.Method != "notification" {
		t.Fatalf("playback notification=%+v err=%v", event, err)
	}
	if err := s.SendICE(ctx, ICECandidateRequest{Candidate: "candidate:synthetic", MLineIndex: 0}); err != nil {
		t.Fatal(err)
	}
	if m := <-writes; m.Method != "ice" {
		t.Fatalf("method=%s", m.Method)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if m := <-writes; m.Method != "close" {
		t.Fatalf("method=%s", m.Method)
	}
}

func TestPushReplayFailureAndClosure(t *testing.T) {
	c, writes := replayConnection(t)
	if _, err := c.SubscribePush(context.Background(), nil); err == nil {
		t.Fatal("empty filters accepted")
	}
	filter := PushFilter{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}
	result := make(chan error, 1)
	go func() { _, err := c.SubscribePush(context.Background(), []PushFilter{filter}); result <- err }()
	request := <-writes
	bad := capturedFrame(t, "push_subscription_ack", "server_to_client")
	bad.DialogID = request.DialogID
	bad.Body = json.RawMessage(`{"status":"denied"}`)
	c.route(bad)
	if err := <-result; err == nil {
		t.Fatal("rejected subscription accepted")
	}
}

func TestPlaybackReplayValidation(t *testing.T) {
	c, writes := replayConnection(t)
	for _, req := range []StartPlaybackRequest{{DeviceID: "bad"}, {DeviceID: "1000", Offer: SessionDescription{Type: "answer", SDP: "x"}}, {DeviceID: "1000", Offer: SessionDescription{Type: "offer", SDP: "bad"}}} {
		if _, err := c.StartPlayback(context.Background(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	frame := capturedFrame(t, "playback", "client_to_server")
	var offer struct {
		SDP string `json:"sdp"`
	}
	_ = json.Unmarshal(frame.Body, &offer)
	result := make(chan error, 1)
	go func() {
		_, err := c.StartPlayback(context.Background(), StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: "offer", SDP: offer.SDP}})
		result <- err
	}()
	request := <-writes
	bad := capturedFrame(t, "sdp", "server_to_client")
	bad.DialogID = request.DialogID
	bad.Body = json.RawMessage(`{"doorbot_id":999,"session_id":"bad","type":"answer","sdp":"x"}`)
	c.route(bad)
	if err := <-result; err == nil {
		t.Fatal("invalid answer accepted")
	}
}

func TestPushReplayIdentityAndReceiveCancellation(t *testing.T) {
	c, writes := replayConnection(t)
	filters := []PushFilter{{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}}
	ready := make(chan *PushSubscription, 1)
	go func() { s, _ := c.SubscribePush(context.Background(), filters); ready <- s }()
	request := <-writes
	replayReply(t, c, request, "push_subscription_ack")
	s := <-ready
	heartbeatDone := make(chan struct{})
	go func() { s.heartbeatAt(100 * time.Millisecond); close(heartbeatDone) }()
	select {
	case m := <-writes:
		if m.Method != "push_heartbeat" {
			t.Fatalf("method=%s", m.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("missing push heartbeat")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Receive(ctx); err != context.Canceled {
		t.Fatalf("receive cancellation=%v", err)
	}
	wrong := capturedFrame(t, "push_event", "server_to_client")
	wrong.DialogID = request.DialogID
	wrong.Body = json.RawMessage(`{"subscription_id":"other","payload":{},"notification_scope":"event","notification_type":"shoulder_tap"}`)
	c.route(wrong)
	ctx2, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err := s.Receive(ctx2); err == nil {
		t.Fatal("foreign event accepted")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	<-heartbeatDone
	<-writes
	if _, err := s.Receive(context.Background()); err != signaling.ErrClosed {
		t.Fatalf("closed receive=%v", err)
	}
}

func TestPlaybackReplayReceiveCancellation(t *testing.T) {
	c, writes := replayConnection(t)
	frame := capturedFrame(t, "playback", "client_to_server")
	var offer struct {
		SDP string `json:"sdp"`
	}
	_ = json.Unmarshal(frame.Body, &offer)
	ready := make(chan *PlaybackSession, 1)
	go func() {
		s, _ := c.StartPlayback(context.Background(), StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: "offer", SDP: offer.SDP}})
		ready <- s
	}()
	request := <-writes
	replayReply(t, c, request, "sdp")
	s := <-ready
	keepaliveDone := make(chan struct{})
	go func() { s.keepalive(100 * time.Millisecond); close(keepaliveDone) }()
	select {
	case m := <-writes:
		if m.Method != "ping" {
			t.Fatalf("method=%s", m.Method)
		}
	case <-time.After(time.Second):
		t.Fatal("missing playback ping")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Receive(ctx); err != context.Canceled {
		t.Fatalf("receive cancellation=%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	<-keepaliveDone
	<-writes
	if _, err := s.Receive(context.Background()); err != signaling.ErrClosed {
		t.Fatalf("closed receive=%v", err)
	}
}

func TestSignalingExtraValidationBeforeWire(t *testing.T) {
	c, writes := replayConnection(t)
	if err := c.sendTyped(context.Background(), protocol.MethodNotification, "dialog", "", make(chan int)); err == nil {
		t.Fatal("unmarshalable body accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	filter := PushFilter{FilterIdentifier: "one", NotificationScope: "event", NotificationType: "shoulder_tap"}
	if _, err := c.SubscribePush(ctx, []PushFilter{filter}); err != context.Canceled {
		t.Fatalf("canceled subscribe=%v", err)
	}
	frame := capturedFrame(t, "playback", "client_to_server")
	var offer struct {
		SDP string `json:"sdp"`
	}
	_ = json.Unmarshal(frame.Body, &offer)
	if _, err := c.StartPlayback(ctx, StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: "offer", SDP: offer.SDP}}); err != context.Canceled {
		t.Fatalf("canceled playback=%v", err)
	}
	select {
	case m := <-writes:
		t.Fatalf("unexpected write %s", m.Method)
	default:
	}
	c.mu.Lock()
	c.closed = true
	c.terminal = signaling.ErrClosed
	c.mu.Unlock()
	if _, _, err := c.registerChannel(); err != signaling.ErrClosed {
		t.Fatalf("closed registration=%v", err)
	}
}

func TestPlaybackCapturedPongAndRemoteClose(t *testing.T) {
	c, writes := replayConnection(t)
	frame := capturedFrame(t, "playback", "client_to_server")
	var offer struct {
		SDP string `json:"sdp"`
	}
	_ = json.Unmarshal(frame.Body, &offer)
	ready := make(chan *PlaybackSession, 1)
	go func() {
		s, _ := c.StartPlayback(context.Background(), StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: "offer", SDP: offer.SDP}})
		ready <- s
	}()
	request := <-writes
	replayReply(t, c, request, "sdp")
	s := <-ready
	before := s.lastPong.Load()
	pong := capturedFrame(t, "pong", "server_to_client")
	pong.DialogID = request.DialogID
	pong.Body = json.RawMessage(`{"doorbot_id":999,"session_id":"wrong"}`)
	c.route(pong)
	if s.lastPong.Load() != before {
		t.Fatal("foreign pong refreshed playback")
	}
	pong.Body = mustJSON(map[string]any{"doorbot_id": 1000, "session_id": s.id})
	c.route(pong)
	if s.lastPong.Load() < before {
		t.Fatal("matching pong did not refresh playback")
	}
	closeFrame := signaling.Message{Method: "close", DialogID: request.DialogID}
	c.route(closeFrame)
	if _, err := s.Receive(context.Background()); err != signaling.ErrClosed {
		t.Fatalf("remote close=%v", err)
	}
}

func TestPlaybackMissingPongTerminates(t *testing.T) {
	c, writes := replayConnection(t)
	frame := capturedFrame(t, "playback", "client_to_server")
	var offer struct {
		SDP string `json:"sdp"`
	}
	_ = json.Unmarshal(frame.Body, &offer)
	ready := make(chan *PlaybackSession, 1)
	go func() {
		s, _ := c.StartPlayback(context.Background(), StartPlaybackRequest{DeviceID: "1000", Offer: SessionDescription{Type: "offer", SDP: offer.SDP}})
		ready <- s
	}()
	request := <-writes
	replayReply(t, c, request, "sdp")
	s := <-ready
	s.lastPong.Store(time.Now().Add(-time.Minute).UnixNano())
	finished := make(chan struct{})
	go func() { s.keepalive(time.Millisecond); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("missing pong did not terminate")
	}
	if _, err := s.Receive(context.Background()); err != signaling.ErrClosed {
		t.Fatalf("timeout receive=%v", err)
	}
}
