package websocket_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
	ringwebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/stretchr/testify/require"
)

func TestAwaitLiveAnswerBoundsEarlyICE(t *testing.T) {
	t.Parallel()

	events := make(chan signaling.Message, signaling.EventQueueCapacity+1)
	for range signaling.EventQueueCapacity + 1 {
		events <- signaling.Message{
			Method: protocol.MethodICE, DialogID: "dialog", RIID: "",
			Body: json.RawMessage(`{"doorbot_id":1001,"session_id":"signal","ice":"candidate:synthetic","mlineindex":0}`),
		}
	}

	state, err := ringwebsocket.AwaitLiveAnswer(
		context.Background(), make(chan struct{}), func() error { return nil },
		events, 1001, func() error { return nil },
	)
	require.ErrorIs(t, err, signaling.ErrBackpressure)
	require.Len(t, state.EarlyICE, signaling.EventQueueCapacity)
}
