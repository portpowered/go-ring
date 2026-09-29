package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
)

type writerTestError string

func (failure writerTestError) Error() string { return string(failure) }

type writerRPCEnvelope struct {
	Command writerRPCCommand `json:"command"`
}

type writerRPCCommand struct {
	Method string `json:"method"`
}

func mustJSON(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	return encoded
}
func TestSignalingWriterPrioritizesSafetyWritesAndRemovesCanceledQueueEntry(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	releaseActive := make(chan struct{})
	writer, writes, activeResult := startWriterWithBlockedWrite(t, done, releaseActive)
	queued := []*signalingWriteRequest{
		queueWriterTestRequest(t, writer, "normal-1", signaling.Message{Method: "normal-1"}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)

	go func() { canceled <- writer.Send(ctx, signaling.Message{Method: "canceled-normal"}) }()

	waitForNormalQueueCount(t, writer, 2)

	cancel()

	err := <-canceled

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation = %v", err)
	}

	writer.mu.Lock()
	normalCount := len(writer.normal)
	writer.mu.Unlock()

	if normalCount != 1 {
		t.Fatalf("canceled request remained queued; normal count=%d", normalCount)
	}

	queued = append(
		queued,
		queueWriterTestRequest(t, writer, "normal-2", signaling.Message{Method: "normal-2"}),
		queueWriterTestRequest(t, writer, "priority-ping", signaling.Message{Method: protocol.MethodPing}),
		queueWriterTestRequest(t, writer, "priority-pong", signaling.Message{Method: protocol.MethodPong}),
		queueWriterTestRequest(t, writer, "priority-close", signaling.Message{Method: protocol.MethodClose}),
		queueWriterTestRequest(
			t,
			writer,
			"priority-stop",
			signaling.Message{
				Method: protocol.MethodRPC,
				Body: mustJSON(
					map[string]any{
						"command": map[string]any{
							"method": protocol.RPCTiltContinuous,
							"params": map[string]any{"speed": 0},
						},
					},
				),
			},
		),
		queueWriterTestRequest(
			t,
			writer,
			"priority-stop-2",
			signaling.Message{
				Method: protocol.MethodRPC,
				Body: mustJSON(
					map[string]any{
						"command": map[string]any{
							"method": protocol.RPCPanContinuous,
							"params": map[string]any{"speed": 0},
						},
					},
				),
			},
		),
	)

	close(releaseActive)
	assertWriterSendSucceeded(t, <-activeResult)

	expected := []string{
		protocol.MethodPing,
		protocol.MethodPong,
		protocol.MethodClose,
		protocol.RPCTiltContinuous,
		"normal-1",
		protocol.RPCPanContinuous,
		"normal-2",
	}
	assertSignalingWriterOrder(t, writes, expected)
	assertSignalingWriterRequestsSucceeded(t, queued)

	close(done)

	select {
	case <-writer.finished:
	case <-time.After(time.Second):
		t.Fatal("writer goroutine did not exit")
	}
}

func startWriterWithBlockedWrite(
	t *testing.T,
	done <-chan struct{},
	release <-chan struct{},
) (*SignalingWriter, <-chan string, <-chan error) {
	t.Helper()

	activeStarted := make(chan struct{})
	writes := make(chan string, 16)

	writer := NewSignalingWriter(done, func(ctx context.Context, message signaling.Message) error {
		name := message.Method

		if message.Method == protocol.MethodRPC {
			var body writerRPCEnvelope

			_ = json.Unmarshal(message.Body, &body)
			name = body.Command.Method
		}

		writes <- name

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

	go writer.Run()

	activeResult := make(chan error, 1)

	go func() { activeResult <- writer.Send(context.Background(), signaling.Message{Method: "active"}) }()

	select {
	case <-activeStarted:
	case <-time.After(time.Second):
		t.Fatal("active write did not start")
	}

	if got := <-writes; got != "active" {
		t.Fatalf("first write = %q", got)
	}

	return writer, writes, activeResult
}

func queueWriterTestRequest(
	t *testing.T,
	writer *SignalingWriter,
	name string,
	message signaling.Message,
) *signalingWriteRequest {
	t.Helper()

	request := &signalingWriteRequest{
		ctx:     context.Background(),
		message: message,
		result:  make(chan error, 1),
	}
	if !writer.queue(request) {
		t.Fatalf("failed to queue %s", name)
	}

	return request
}

func waitForNormalQueueCount(t *testing.T, writer *SignalingWriter, expected int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)

	for {
		writer.mu.Lock()
		present := len(writer.normal) == expected
		writer.mu.Unlock()

		if present {
			return
		}

		if time.Now().After(deadline) {
			t.Fatal("cancellable write was not queued")
		}

		time.Sleep(time.Millisecond)
	}
}

func assertSignalingWriterOrder(t *testing.T, writes <-chan string, expected []string) {
	t.Helper()

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
}

func assertSignalingWriterRequestsSucceeded(t *testing.T, requests []*signalingWriteRequest) {
	t.Helper()

	for _, request := range requests {
		err := <-request.result
		if err != nil {
			t.Fatalf("queued %s failed: %v", request.message.Method, err)
		}
	}
}

func assertWriterSendSucceeded(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func TestSignalingWriterDrainsOnCloseAndReportsActiveWriteFailure(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	activeStarted := make(chan struct{})
	release := make(chan struct{})

	writer := NewSignalingWriter(done, func(ctx context.Context, message signaling.Message) error {
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
	go writer.Run()

	active := make(chan error, 1)

	go func() { active <- writer.Send(context.Background(), signaling.Message{Method: "active"}) }()

	select {
	case <-activeStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not become active")
	}

	queued := &signalingWriteRequest{
		ctx:     context.Background(),
		message: signaling.Message{Method: "queued"},
		result:  make(chan error, 1),
	}
	if !writer.queue(queued) {
		t.Fatal("could not queue request")
	}

	close(done)
	close(release)

	err := <-active

	if err != nil && !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("active write failed: %v", err)
	}

	err = <-queued.result

	if !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("drained request error = %v", err)
	}

	select {
	case <-writer.finished:
	case <-time.After(time.Second):
		t.Fatal("writer did not stop after close")
	}

	done2 := make(chan struct{})
	failureObserved := make(chan error, 1)

	w2 := NewSignalingWriter(
		done2,
		func(context.Context, signaling.Message) error { return writerTestError("write failed") },
		func(err error) { failureObserved <- err; close(done2) },
	)
	go w2.Run()

	err = w2.Send(context.Background(), signaling.Message{Method: "will_fail"})
	if err == nil {
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

func TestSignalingWriterPreservesCanceledQueuedRequestWhenStopped(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	close(done)

	writes := 0
	writer := NewSignalingWriter(done, func(context.Context, signaling.Message) error {
		writes++

		return nil
	}, nil)

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()

	canceled := queueWriterTestRequest(
		t,
		writer,
		"canceled",
		signaling.Message{Method: "canceled"},
	)
	canceled.ctx = canceledContext
	closed := queueWriterTestRequest(t, writer, "closed", signaling.Message{Method: "closed"})

	writer.Run()

	err := <-canceled.result
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled queued request error = %v", err)
	}

	err = <-closed.result
	if !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("uncanceled queued request error = %v", err)
	}

	if writes != 0 {
		t.Fatalf("stopped writer sent %d queued messages", writes)
	}

	err = writer.Send(context.Background(), signaling.Message{Method: "after-close"})
	if !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("send after stop error = %v", err)
	}

	writer.mu.Lock()
	queuedCount := len(writer.priority) + len(writer.normal)
	writer.mu.Unlock()

	if queuedCount != 0 {
		t.Fatalf("send after stop left %d queued messages", queuedCount)
	}
}

func TestSignalingWriterWakesForSendAfterIdleWake(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	writes := make(chan string, 1)

	writer := NewSignalingWriter(done, func(_ context.Context, message signaling.Message) error {
		writes <- message.Method

		return nil
	}, nil)
	writer.wake <- struct{}{}

	go writer.Run()

	deadline := time.Now().Add(time.Second)
	for len(writer.wake) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("writer did not consume the pending idle wake")
		}

		time.Sleep(time.Millisecond)
	}

	err := writer.Send(context.Background(), signaling.Message{Method: "after-idle"})
	if err != nil {
		t.Fatalf("send after idle wake failed: %v", err)
	}

	select {
	case method := <-writes:
		if method != "after-idle" {
			t.Fatalf("written method = %q, want after-idle", method)
		}
	case <-time.After(time.Second):
		t.Fatal("writer did not resume after the idle wake")
	}

	close(done)

	select {
	case <-writer.Finished():
	case <-time.After(time.Second):
		t.Fatal("writer did not stop after close")
	}
}

func TestSignalingWriterBoundsQueueAndClassifiesSafetyTraffic(t *testing.T) {
	t.Parallel()

	writer := NewSignalingWriter(make(chan struct{}), func(context.Context, signaling.Message) error { return nil }, nil)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := writer.Send(canceledCtx, signaling.Message{Method: "already_canceled"})

	if !ringerrors.IsConnectionError(err) || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled send error = %v", err)
	}

	for index := range signaling.SignalingWriteQueueCapacity {
		if !writer.queue(
			&signalingWriteRequest{
				ctx:     context.Background(),
				message: signaling.Message{Method: "normal"},
				result:  make(chan error, 1),
			},
		) {
			t.Fatalf("queue rejected request %d", index)
		}
	}

	if writer.queue(
		&signalingWriteRequest{
			ctx:     context.Background(),
			message: signaling.Message{Method: "overflow"},
			result:  make(chan error, 1),
		},
	) {
		t.Fatal("queue exceeded configured bound")
	}

	writer.drain()

	zero, positive := 0.0, 0.5

	tests := []struct {
		message  signaling.Message
		priority bool
	}{
		{signaling.Message{Method: protocol.MethodPing}, true},
		{signaling.Message{Method: protocol.MethodClose}, true},
		{signaling.Message{Method: protocol.MethodStreamOptions}, false},
		{
			signaling.Message{
				Method: protocol.MethodRPC,
				Body: mustJSON(
					map[string]any{
						"command": map[string]any{
							"method": protocol.RPCTiltContinuous,
							"params": map[string]any{"speed": &zero},
						},
					},
				),
			},
			true,
		},
		{
			signaling.Message{
				Method: protocol.MethodRPC,
				Body: mustJSON(
					map[string]any{
						"command": map[string]any{
							"method": protocol.RPCTiltContinuous,
							"params": map[string]any{"speed": &positive},
						},
					},
				),
			},
			false,
		},
		{signaling.Message{Method: protocol.MethodRPC, Body: json.RawMessage(`{`)}, false},
		{
			signaling.Message{
				Method: protocol.MethodRPC,
				Body: mustJSON(
					map[string]any{
						"command": map[string]any{
							"method": protocol.RPCPanStep,
							"params": map[string]any{"speed": &zero},
						},
					},
				),
			},
			false,
		},
	}
	for _, tt := range tests {
		if got := prioritySignalingMessage(tt.message); got != tt.priority {
			t.Fatalf("priority(%s)=%v want %v", tt.message.Method, got, tt.priority)
		}
	}
}

func TestSameSessionSafetyWritesBypassQueuedControls(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	activeStarted, release := make(chan struct{}), make(chan struct{})
	writes := make(chan string, 8)

	writer := NewSignalingWriter(done, func(ctx context.Context, m signaling.Message) error {
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
	go writer.Run()

	session, err := signaling.NewSession(
		context.Background(),
		signaling.SessionConfig{
			DeviceID:  7,
			DialogID:  "dialog",
			SignalID:  "signal",
			ControlID: "control",
			Heartbeat: time.Minute,
			Send:      writer.Send,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	var releaseOnce, doneOnce sync.Once

	defer func() {
		releaseOnce.Do(func() { close(release) })

		_ = session.Close()

		doneOnce.Do(func() { close(done) })
		<-writer.finished
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
			writer.mu.Lock()
			ready := len(writer.priority) == priority && len(writer.normal) == normal
			writer.mu.Unlock()

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
		err := <-results
		if err != nil {
			t.Fatal(err)
		}
	}
}
