package replay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/portpowered/go-ring/pkg/generatedsignaling"
	"github.com/portpowered/go-ring/pkg/ring"
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
