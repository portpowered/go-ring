package replay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/generatedsignaling"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

// Synthetic paired transcript: candidates precede session_created, the answer,
// activation and camera_started. A foreign session's candidate must stay absent.
func TestLiveSessionPreservesEarlyRemoteICE(t *testing.T) {
	t.Parallel()

	conn, peer, transport := openProductionTranscript(t, "live-early-ice.json")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID: "1000", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
		AudioEnabled: false, VideoEnabled: true, MaxAge: 0, ICEMode: ring.ICENonTrickle,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })

	for _, expected := range []string{"candidate:1", "candidate:2", "candidate:3", "candidate:4"} {
		for {
			event, receiveErr := session.Receive(ctx)
			require.NoError(t, receiveErr)

			if event.Method != "ice" {
				continue
			}

			var candidate generatedsignaling.ServerIceBody

			require.NoError(t, json.Unmarshal(event.Body, &candidate))
			require.Equal(t, expected, candidate.Ice)

			break
		}
	}

	require.NoError(t, session.Close())
	require.NoError(t, peer.AssertComplete(3*time.Second))
	require.NoError(t, transport.AssertConsumed())
}

func TestLiveSessionRejectsMalformedEarlyICE(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)
	peerDone := make(chan struct{})
	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		defer close(peerDone)

		created := recordedSessionFrame(t, captured["session_created"], dialog)
		if !writeFrameIfConnected(connection, created) {
			return
		}

		body := replayObjectField(t, created, "body")
		badICE := map[string]any{
			"doorbot_id": "invalid", "session_id": body["session_id"], "ice": "candidate:synthetic", "mlineindex": 0,
		}

		if !writeFrameIfConnected(connection, map[string]any{"method": "ice", "dialog_id": dialog, "body": badICE}) ||
			!writeFrameIfConnected(connection, recordedSessionFrame(t, captured["sdp"], dialog)) {
			return
		}

		// Wire validation rejects this identity before session creation and
		// closes the socket; it cannot safely send a device-session close.
		waitForRecordedClientClose(t, connection)
	})

	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
		AudioEnabled: false, VideoEnabled: true, MaxAge: 0, ICEMode: ring.ICENonTrickle,
	})
	// The reader can close the socket before the initial offer's writer
	// acknowledgement is consumed. Both entry-point errors describe failure;
	// the terminal connection cause must still identify the malformed input.
	require.True(t, ringapimodels.IsConnectionError(err) || ringapimodels.IsClosedError(err), "%v", err)
	require.Nil(t, session)

	select {
	case <-peerDone:
	case <-ctx.Done():
		t.Fatal("malformed ICE peer did not observe socket close")
	}

	terminalErr := conn.Err()
	require.True(t, ringapimodels.IsConnectionError(terminalErr), "%v", terminalErr)

	var malformed *json.UnmarshalTypeError

	require.ErrorAs(t, terminalErr, &malformed)
	require.Contains(t, malformed.Field, "doorbot_id")
	require.NoError(t, conn.Close())
}

// This synthetic mutation overfills the pre-answer queue through the actual
// public WebSocket client. No candidate is silently dropped to admit a session.
func TestLiveSessionEarlyICEOverflow(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)
	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		created := recordedSessionFrame(t, captured["session_created"], dialog)
		if !writeFrameIfConnected(connection, created) {
			return
		}

		body := replayObjectField(t, created, "body")
		body["ice"], body["mlineindex"] = "candidate:synthetic", 0

		for range signaling.EventQueueCapacity + 1 {
			if !writeFrameIfConnected(connection, map[string]any{"method": "ice", "dialog_id": dialog, "body": body}) {
				return
			}
		}

		var closeFrame map[string]any

		if connection.ReadJSON(&closeFrame) != nil || closeFrame["method"] != "close" {
			t.Error("SDK did not close the overflowed negotiation")

			return
		}

		closedBody := replayObjectField(t, closeFrame, "body")
		if closedBody["session_id"] != body["session_id"] || closedBody["doorbot_id"] != body["doorbot_id"] {
			t.Error("overflow cleanup closed the wrong device session")
		}

		waitForRecordedClientClose(t, connection)
	})

	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)

	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
		AudioEnabled: false, VideoEnabled: true, MaxAge: 0, ICEMode: ring.ICENonTrickle,
	})
	require.ErrorIs(t, err, ring.ErrSessionBackpressure)
	require.Nil(t, session)
}
