package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
)

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
func TestSignalingWriterPrioritizesSafetyWritesAndRemovesCanceledQueueEntry(t *testing.T) {
	done := make(chan struct{})
	releaseActive := make(chan struct{})
	activeStarted := make(chan struct{})
	writes := make(chan string, 16)
	w := NewSignalingWriter(done, func(ctx context.Context, m signaling.Message) error {
		name := m.Method
		if m.Method == protocol.MethodRPC {
			var body struct {
				Command struct {
					Method string `json:"method"`
				} `json:"command"`
			}
			_ = json.Unmarshal(m.Body, &body)
			name = body.Command.Method
		}
		writes <- name
		if m.Method == "active" {
			close(activeStarted)
			select {
			case <-releaseActive:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, nil)
	go w.Run()
	activeResult := make(chan error, 1)
	go func() { activeResult <- w.Send(context.Background(), signaling.Message{Method: "active"}) }()
	select {
	case <-activeStarted:
	case <-time.After(time.Second):
		t.Fatal("active write did not start")
	}
	if got := <-writes; got != "active" {
		t.Fatalf("first write = %q", got)
	}

	queue := func(method string, message signaling.Message) *signalingWriteRequest {
		r := &signalingWriteRequest{ctx: context.Background(), message: message, result: make(chan error, 1)}
		if !w.queue(r) {
			t.Fatalf("failed to queue %s", method)
		}
		return r
	}
	queued := []*signalingWriteRequest{queue("normal-1", signaling.Message{Method: "normal-1"})}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() { canceled <- w.Send(ctx, signaling.Message{Method: "canceled-normal"}) }()
	deadline := time.Now().Add(time.Second)
	for {
		w.mu.Lock()
		present := len(w.normal) == 2
		w.mu.Unlock()
		if present {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellable write was not queued")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation = %v", err)
	}
	w.mu.Lock()
	normalCount := len(w.normal)
	w.mu.Unlock()
	if normalCount != 1 {
		t.Fatalf("canceled request remained queued; normal count=%d", normalCount)
	}
	queued = append(queued,
		queue("normal-2", signaling.Message{Method: "normal-2"}),
		queue("priority-ping", signaling.Message{Method: protocol.MethodPing}),
		queue("priority-pong", signaling.Message{Method: protocol.MethodPong}),
		queue("priority-close", signaling.Message{Method: protocol.MethodClose}),
		queue("priority-stop", signaling.Message{Method: protocol.MethodRPC, Body: mustJSON(map[string]any{"command": map[string]any{"method": protocol.RPCTiltContinuous, "params": map[string]any{"speed": 0}}})}),
		queue("priority-stop-2", signaling.Message{Method: protocol.MethodRPC, Body: mustJSON(map[string]any{"command": map[string]any{"method": protocol.RPCPanContinuous, "params": map[string]any{"speed": 0}}})}),
	)
	close(releaseActive)
	if err := <-activeResult; err != nil {
		t.Fatal(err)
	}
	expected := []string{protocol.MethodPing, protocol.MethodPong, protocol.MethodClose, protocol.RPCTiltContinuous, "normal-1", protocol.RPCPanContinuous, "normal-2"}
	for _, want := range expected {
		select {
		case got := <-writes:
			if got != want {
				t.Fatalf("write order got %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %q", want)
		}
	}
	for _, req := range queued {
		if err := <-req.result; err != nil {
			t.Fatalf("queued %s failed: %v", req.message.Method, err)
		}
	}
	close(done)
	select {
	case <-w.finished:
	case <-time.After(time.Second):
		t.Fatal("writer goroutine did not exit")
	}
}

func TestSignalingWriterDrainsOnCloseAndReportsActiveWriteFailure(t *testing.T) {
	done := make(chan struct{})
	activeStarted := make(chan struct{})
	release := make(chan struct{})
	w := NewSignalingWriter(done, func(ctx context.Context, message signaling.Message) error {
		if message.Method == "active" {
			close(activeStarted)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, nil)
	go w.Run()
	active := make(chan error, 1)
	go func() { active <- w.Send(context.Background(), signaling.Message{Method: "active"}) }()
	select {
	case <-activeStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not become active")
	}
	queued := &signalingWriteRequest{ctx: context.Background(), message: signaling.Message{Method: "queued"}, result: make(chan error, 1)}
	if !w.queue(queued) {
		t.Fatal("could not queue request")
	}
	close(done)
	close(release)
	if err := <-active; err != nil && !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("active write failed: %v", err)
	}
	if err := <-queued.result; !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("drained request error = %v", err)
	}
	select {
	case <-w.finished:
	case <-time.After(time.Second):
		t.Fatal("writer did not stop after close")
	}

	done2 := make(chan struct{})
	failureObserved := make(chan error, 1)
	w2 := NewSignalingWriter(done2, func(context.Context, signaling.Message) error { return errors.New("write failed") }, func(err error) { failureObserved <- err; close(done2) })
	go w2.Run()
	if err := w2.Send(context.Background(), signaling.Message{Method: "will_fail"}); err == nil {
		t.Fatal("write failure was swallowed")
	}
	select {
	case <-failureObserved:
	case <-time.After(time.Second):
		t.Fatal("write failure callback did not run")
	}
	select {
	case <-w2.finished:
	case <-time.After(time.Second):
		t.Fatal("failed writer goroutine did not stop")
	}
}

func TestSignalingWriterBoundsQueueAndClassifiesSafetyTraffic(t *testing.T) {
	w := NewSignalingWriter(make(chan struct{}), func(context.Context, signaling.Message) error { return nil }, nil)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.Send(canceledCtx, signaling.Message{Method: "already_canceled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled send error = %v", err)
	}
	for i := 0; i < signaling.SignalingWriteQueueCapacity; i++ {
		if !w.queue(&signalingWriteRequest{ctx: context.Background(), message: signaling.Message{Method: "normal"}, result: make(chan error, 1)}) {
			t.Fatalf("queue rejected request %d", i)
		}
	}
	if w.queue(&signalingWriteRequest{ctx: context.Background(), message: signaling.Message{Method: "overflow"}, result: make(chan error, 1)}) {
		t.Fatal("queue exceeded configured bound")
	}
	w.drain()
	zero, positive := 0.0, 0.5
	tests := []struct {
		message  signaling.Message
		priority bool
	}{
		{signaling.Message{Method: protocol.MethodPing}, true},
		{signaling.Message{Method: protocol.MethodClose}, true},
		{signaling.Message{Method: protocol.MethodStreamOptions}, false},
		{signaling.Message{Method: protocol.MethodRPC, Body: mustJSON(map[string]any{"command": map[string]any{"method": protocol.RPCTiltContinuous, "params": map[string]any{"speed": &zero}}})}, true},
		{signaling.Message{Method: protocol.MethodRPC, Body: mustJSON(map[string]any{"command": map[string]any{"method": protocol.RPCTiltContinuous, "params": map[string]any{"speed": &positive}}})}, false},
		{signaling.Message{Method: protocol.MethodRPC, Body: json.RawMessage(`{`)}, false},
		{signaling.Message{Method: protocol.MethodRPC, Body: mustJSON(map[string]any{"command": map[string]any{"method": protocol.RPCPanStep, "params": map[string]any{"speed": &zero}}})}, false},
	}
	for _, tt := range tests {
		if got := prioritySignalingMessage(tt.message); got != tt.priority {
			t.Fatalf("priority(%s)=%v want %v", tt.message.Method, got, tt.priority)
		}
	}
}

func TestSameSessionSafetyWritesBypassQueuedControls(t *testing.T) {
	done := make(chan struct{})
	activeStarted, release := make(chan struct{}), make(chan struct{})
	writes := make(chan string, 8)
	w := NewSignalingWriter(done, func(ctx context.Context, m signaling.Message) error {
		writes <- m.Method
		if m.Method == "mic_enable" {
			close(activeStarted)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, nil)
	go w.Run()
	session, err := signaling.NewSession(context.Background(), signaling.SessionConfig{DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute, Send: w.Send})
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce, doneOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(release) })
		_ = session.Close()
		doneOnce.Do(func() { close(done) })
		<-w.finished
	}()
	results := make(chan error, 4)
	go func() { results <- session.Send(context.Background(), "mic_enable", map[string]any{"enabled": true}) }()
	select {
	case <-activeStarted:
	case <-time.After(time.Second):
		t.Fatal("first session write did not become active")
	}
	waitQueue := func(priority, normal int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			w.mu.Lock()
			ready := len(w.priority) == priority && len(w.normal) == normal
			w.mu.Unlock()
			if ready {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("queue sizes priority/normal did not reach %d/%d", priority, normal)
			}
			time.Sleep(time.Millisecond)
		}
	}
	go func() { results <- session.Send(context.Background(), "stream_options", nil) }()
	waitQueue(0, 1)
	go func() { results <- session.Send(context.Background(), protocol.MethodPing, nil) }()
	waitQueue(1, 1)
	go func() { results <- session.Send(context.Background(), protocol.MethodClose, nil) }()
	waitQueue(2, 1)
	releaseOnce.Do(func() { close(release) })
	expected := []string{"mic_enable", protocol.MethodPing, protocol.MethodClose, "stream_options"}
	for _, want := range expected {
		select {
		case got := <-writes:
			if got != want {
				t.Fatalf("same-session write order got %q want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
	for range 4 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
