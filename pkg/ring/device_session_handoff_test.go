package ring

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/generatedsignaling"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
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

// Wire validation normally rejects malformed identity fields first. This
// defensive boundary test seeds already-decoded events to verify the SDK still
// rejects a newly created session if replay into its core is rejected.
func TestNegotiatedSessionRejectsInvalidRetainedEvent(t *testing.T) {
	t.Parallel()

	events := make(chan signaling.Message, 3)
	events <- signaling.Message{
		Method: protocol.MethodSessionCreated, DialogID: "dialog", RIID: "",
		Body: mustJSON(generatedsignaling.SessionCreatedBody{
			DoorbotId: 7, SessionId: "signal", AdditionalProperties: nil,
		}),
	}

	events <- signaling.Message{
		Method: protocol.MethodICE, DialogID: "dialog", RIID: "", Body: json.RawMessage(`{"doorbot_id":"invalid"}`),
	}

	events <- signaling.Message{
		Method: protocol.MethodSDP, DialogID: "dialog", RIID: "",
		Body: mustJSON(generatedsignaling.LiveAnswerBody{
			DoorbotId: 7, SessionId: "signal", Sdp: strings.Replace(publicTestOffer, "recvonly", "sendonly", 1),
			ReservedType: "answer", AdditionalProperties: nil,
			SessionInfo: &generatedsignaling.LiveAnswerInfo{SessionId: "control", PingInterval: 10, AdditionalProperties: nil},
		}),
	}

	connection := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
	negotiation := &deviceSessionNegotiation{
		started: time.Now(), id: 7, maxAge: time.Minute, cancel: nil,
		dialog: "dialog", events: events, signalID: "", riid: "",
		startedSuccess: false, deadlineError: nil,
	}
	request := StartDeviceSessionRequest{
		DeviceID: "7", Offer: SessionDescription{Type: SDPTypeOffer, SDP: publicTestOffer},
		VideoEnabled: true, AudioEnabled: false, MaxAge: time.Minute, ICEMode: ICENonTrickle,
	}
	session, err := connection.createNegotiatedDeviceSession(
		context.Background(), context.Background(), request, negotiation,
	)
	require.Nil(t, session)
	require.True(t, ringapimodels.IsConnectionError(err))
}

func TestActivatedSessionHandoffRejectsTerminalState(t *testing.T) {
	t.Parallel()

	const malformedTail = "malformed tail"

	const fullEventQueue = "full event queue"

	for _, scenario := range []string{"closed connection", "pending close", malformedTail, fullEventQueue} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			session, core := newPublicTestSession(t)
			t.Cleanup(func() { _ = core.Close() })

			connection := session.connection
			events := make(chan signaling.Message, 1)
			negotiation := &deviceSessionNegotiation{
				started: time.Time{}, id: 7, maxAge: 0, cancel: nil,
				dialog: "dialog", events: events, signalID: "signal", riid: "",
				startedSuccess: false, deadlineError: nil,
			}
			connection.pending = map[string]chan signaling.Message{"dialog": events}
			delete(connection.sessions, "dialog")

			message := handoffTestICE("pending")

			switch scenario {
			case "closed connection":
				connection.closed = true
			case "pending close":
				message.Method = protocol.MethodClose
			case malformedTail:
				message.Body = json.RawMessage(`{"doorbot_id":"invalid"}`)
			case fullEventQueue:
				for range signaling.EventQueueCapacity {
					require.NoError(t, core.Handle(message))
				}
			}

			events <- message

			err := connection.registerActivatedDeviceSession(negotiation, session)
			require.Error(t, err)

			switch scenario {
			case fullEventQueue:
				require.ErrorIs(t, err, signaling.ErrBackpressure)
			case malformedTail:
				require.True(t, ringapimodels.IsConnectionError(err))
			default:
				require.ErrorIs(t, err, signaling.ErrClosed)
			}

			connection.mu.Lock()
			registered := connection.sessions["dialog"] != nil
			connection.mu.Unlock()
			require.False(t, registered)
		})
	}
}

func TestPendingNegotiationQueueOverflowClosesConnection(t *testing.T) {
	t.Parallel()

	conn, _ := newBlockedSignalingWebSocket(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	connection := &SignalingConnection{
		conn: conn, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		pending: map[string]chan signaling.Message{"dialog": make(chan signaling.Message)},
	}
	connection.route(handoffTestICE("overflow"))
	require.True(t, ringapimodels.IsConnectionError(connection.Err()))
	require.ErrorIs(t, ctx.Err(), context.Canceled)

	select {
	case <-connection.done:
	default:
		t.Fatal("overflowed connection was not closed")
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
