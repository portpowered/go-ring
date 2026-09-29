package websocket

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/portpowered/go-ring/internal/generatedsignaling"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
)

type signalingWriteRequest struct {
	ctx     context.Context
	message signaling.Message
	result  chan error
}

// SignalingWriter bounds outgoing frames and prioritizes close, heartbeat, and PTZ stop.
type SignalingWriter struct {
	done      <-chan struct{}
	wake      chan struct{}
	finished  chan struct{}
	write     func(context.Context, signaling.Message) error
	onFailure func(error)
	mu        sync.Mutex
	priority  []*signalingWriteRequest
	normal    []*signalingWriteRequest
	streak    int
}

// NewSignalingWriter constructs a writer that stops when done closes.
func NewSignalingWriter(
	done <-chan struct{},
	write func(context.Context, signaling.Message) error,
	onFailure func(error),
) *SignalingWriter {
	return &SignalingWriter{
		done:      done,
		wake:      make(chan struct{}, 1),
		finished:  make(chan struct{}),
		write:     write,
		onFailure: onFailure,
	}
}

// Finished closes after Run drains queued requests.
func (w *SignalingWriter) Finished() <-chan struct{} { return w.finished }

// Send queues one frame and waits for the write or cancellation.
func (w *SignalingWriter) Send(ctx context.Context, message signaling.Message) error {
	err := ctx.Err()
	if err != nil {
		return wrapSignalingContextError(err)
	}

	request := &signalingWriteRequest{ctx: ctx, message: message, result: make(chan error, 1)}
	if !w.queue(request) {
		return signaling.ErrBackpressure
	}

	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		w.remove(request)

		return wrapSignalingContextError(ctx.Err())
	case <-w.done:
		w.remove(request)

		return signaling.ErrClosed
	}
}

// Run services the writer queue until done closes.
func (w *SignalingWriter) Run() {
	defer close(w.finished)

	for {
		select {
		case <-w.done:
			w.drain()

			return
		default:
		}

		request := w.next()
		if request == nil {
			select {
			case <-w.wake:
				continue
			case <-w.done:
				w.drain()

				return
			}
		}

		select {
		case <-w.done:
			err := request.ctx.Err()
			if err == nil {
				err = signaling.ErrClosed
			} else {
				err = wrapSignalingContextError(err)
			}

			request.result <- err

			w.drain()

			return
		default:
		}

		err := request.ctx.Err()
		if err == nil {
			err = w.write(request.ctx, request.message)
		} else {
			err = wrapSignalingContextError(err)
		}

		request.result <- err

		if err != nil && request.ctx.Err() == nil && w.onFailure != nil {
			w.onFailure(err)
		}
	}
}

func (w *SignalingWriter) queue(request *signalingWriteRequest) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.priority)+len(w.normal) >= signaling.SignalingWriteQueueCapacity {
		return false
	}

	if prioritySignalingMessage(request.message) {
		w.priority = append(w.priority, request)
	} else {
		w.normal = append(w.normal, request)
	}

	select {
	case w.wake <- struct{}{}:
	default:
	}

	return true
}

func (w *SignalingWriter) remove(request *signalingWriteRequest) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for _, queue := range []*[]*signalingWriteRequest{&w.priority, &w.normal} {
		for i, queued := range *queue {
			if queued == request {
				*queue = append((*queue)[:i], (*queue)[i+1:]...)

				return
			}
		}
	}
}

func (w *SignalingWriter) next() *signalingWriteRequest {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.priority) == 0 && len(w.normal) == 0 {
		return nil
	}

	if len(w.normal) > 0 && (len(w.priority) == 0 || w.streak >= signaling.PriorityWriteBurstLimit) {
		next := w.normal[0]
		w.normal = w.normal[1:]
		w.streak = 0

		return next
	}

	next := w.priority[0]
	w.priority = w.priority[1:]
	w.streak++

	return next
}

func (w *SignalingWriter) drain() {
	w.mu.Lock()
	queued := make([]*signalingWriteRequest, 0, len(w.priority)+len(w.normal))
	queued = append(queued, w.priority...)
	queued = append(queued, w.normal...)
	w.priority, w.normal = nil, nil
	w.mu.Unlock()

	for _, request := range queued {
		err := request.ctx.Err()
		if err == nil {
			err = signaling.ErrClosed
		} else {
			err = wrapSignalingContextError(err)
		}

		request.result <- err
	}
}

func prioritySignalingMessage(message signaling.Message) bool {
	if message.Method == protocol.MethodClose || message.Method == protocol.MethodPing ||
		message.Method == protocol.MethodPong {
		return true
	}

	if message.Method != protocol.MethodRPC {
		return false
	}

	var body generatedsignaling.PtzContinuousCommandBody
	if json.Unmarshal(message.Body, &body) != nil || body.Command == nil || body.Command.Params == nil ||
		body.Command.Method == nil || body.Command.Params.Speed != 0 {
		return false
	}

	method, ok := body.Command.Method.Value().(string)
	if !ok || (method != protocol.RPCPanContinuous && method != protocol.RPCTiltContinuous) {
		return false
	}

	return hasExplicitPTZStop(message.Body, method)
}

func hasExplicitPTZStop(encoded json.RawMessage, method string) bool {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(encoded, &envelope) != nil {
		return false
	}

	var command map[string]json.RawMessage
	if json.Unmarshal(envelope["command"], &command) != nil {
		return false
	}

	var wireMethod string
	if json.Unmarshal(command["method"], &wireMethod) != nil || wireMethod != method {
		return false
	}

	var params map[string]json.RawMessage
	if json.Unmarshal(command["params"], &params) != nil {
		return false
	}

	var speed float64

	return json.Unmarshal(params["speed"], &speed) == nil && speed == 0
}
