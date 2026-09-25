package ring

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
)

type signalingWriteRequest struct {
	ctx     context.Context
	message signaling.Message
	result  chan error
}

type signalingWriter struct {
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

func newSignalingWriter(done <-chan struct{}, write func(context.Context, signaling.Message) error, onFailure func(error)) *signalingWriter {
	return &signalingWriter{done: done, wake: make(chan struct{}, 1), finished: make(chan struct{}), write: write, onFailure: onFailure}
}

func (w *signalingWriter) send(ctx context.Context, message signaling.Message) error {
	if err := ctx.Err(); err != nil {
		return err
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
		return ctx.Err()
	case <-w.done:
		w.remove(request)
		return signaling.ErrClosed
	}
}

func (w *signalingWriter) queue(request *signalingWriteRequest) bool {
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

func (w *signalingWriter) remove(request *signalingWriteRequest) {
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

func (w *signalingWriter) next() *signalingWriteRequest {
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

func (w *signalingWriter) run() {
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
			}
			request.result <- err
			w.drain()
			return
		default:
		}
		err := request.ctx.Err()
		if err == nil {
			err = w.write(request.ctx, request.message)
		}
		request.result <- err
		if err != nil && request.ctx.Err() == nil && w.onFailure != nil {
			w.onFailure(err)
		}
	}
}

func (w *signalingWriter) drain() {
	w.mu.Lock()
	queued := append(w.priority, w.normal...)
	w.priority, w.normal = nil, nil
	w.mu.Unlock()
	for _, request := range queued {
		err := request.ctx.Err()
		if err == nil {
			err = signaling.ErrClosed
		}
		request.result <- err
	}
}

func prioritySignalingMessage(message signaling.Message) bool {
	if message.Method == protocol.MethodClose || message.Method == protocol.MethodPing || message.Method == protocol.MethodPong {
		return true
	}
	if message.Method != protocol.MethodRPC {
		return false
	}
	var body struct {
		Command struct {
			Method string `json:"method"`
			Params struct {
				Speed *float64 `json:"speed"`
			} `json:"params"`
		} `json:"command"`
	}
	if json.Unmarshal(message.Body, &body) != nil || body.Command.Params.Speed == nil || *body.Command.Params.Speed != 0 {
		return false
	}
	return body.Command.Method == protocol.RPCPanContinuous || body.Command.Method == protocol.RPCTiltContinuous
}
