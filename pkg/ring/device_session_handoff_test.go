package ring

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/stretchr/testify/require"
)

func TestActivatedSessionHandoffPreservesPendingTail(t *testing.T) {
	t.Parallel()

	const (
		rounds     = 100
		tailLength = signaling.EventQueueCapacity - 2
	)

	for range rounds {
		session, core := newPublicTestSession(t)
		t.Cleanup(func() { _ = core.Close() })

		connection := session.connection
		events := make(chan signaling.Message, signaling.NegotiationQueueCapacity)
		negotiation := &deviceSessionNegotiation{
			started: time.Time{}, id: 7, maxAge: 0, cancel: nil,
			dialog: "dialog", events: events, signalID: "signal", riid: "",
			startedSuccess: false, deadlineError: nil,
		}
		connection.pending = map[string]chan signaling.Message{"dialog": events}
		delete(connection.sessions, "dialog")

		for range tailLength {
			events <- handoffTestICE("pending")
		}

		late := make(chan struct{})
		go routeAfterHandoff(connection, late)

		require.NoError(t, connection.registerActivatedDeviceSession(negotiation, session))
		<-late

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		for index := 0; index <= tailLength; index++ {
			event, err := session.Receive(ctx)
			require.NoError(t, err)

			expected := "pending"
			if index == tailLength {
				expected = "direct"
			}

			require.Contains(t, string(event.Body), expected)
		}

		cancel()
		require.NoError(t, core.Close())
	}
}

func routeAfterHandoff(connection *SignalingConnection, done chan<- struct{}) {
	defer close(done)

	for {
		connection.mu.Lock()
		registered := connection.sessions["dialog"] != nil
		connection.mu.Unlock()

		if registered {
			connection.route(handoffTestICE("direct"))

			return
		}

		runtime.Gosched()
	}
}

func handoffTestICE(label string) signaling.Message {
	return signaling.Message{
		Method: protocol.MethodICE, DialogID: "dialog", RIID: "",
		Body: json.RawMessage(`{"doorbot_id":7,"session_id":"signal","ice":"` + label + `","mlineindex":0}`),
	}
}
