package replay_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func TestRecordedLiveSessionFrameOrdering(t *testing.T) {
	offer, captured := recordedLiveView(t)
	for _, scenario := range []string{"answer-before-created", "conflicting-created", "premature-camera-start", "camera-start-before-activation-read", "matching-duplicate-created"} {
		t.Run(scenario, func(t *testing.T) {
			conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
				created := recordedSessionFrame(t, captured["session_created"], dialog)
				answer := recordedSessionFrame(t, captured["sdp"], dialog)
				camera := recordedSessionFrame(t, captured["camera_started"], dialog)
				switch scenario {
				case "answer-before-created":
					_ = c.WriteJSON(answer)
					_ = c.WriteJSON(created)
					return
				case "premature-camera-start":
					_ = c.WriteJSON(camera)
				}
				if err := c.WriteJSON(created); err != nil {
					return
				}
				if scenario == "conflicting-created" || scenario == "matching-duplicate-created" {
					duplicate := recordedSessionFrame(t, captured["session_created"], dialog)
					if scenario == "conflicting-created" {
						duplicate["body"].(map[string]any)["session_id"] = "other-session"
					}
					_ = c.WriteJSON(duplicate)
				}
				if err := c.WriteJSON(answer); err != nil {
					return
				}
				if scenario == "conflicting-created" {
					return
				}
				if scenario == "camera-start-before-activation-read" {
					_ = c.WriteJSON(camera)
				}
				if !readActivation(c) {
					return
				}
				if scenario == "matching-duplicate-created" {
					_ = c.WriteJSON(camera)
				}
				_, _, _ = c.ReadMessage()
			})
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}})
			switch scenario {
			case "camera-start-before-activation-read", "matching-duplicate-created":
				require.NoError(t, err)
				require.Equal(t, ring.SessionActive, session.State())
				require.NoError(t, session.Close())
			case "premature-camera-start":
				require.Nil(t, session)
				require.True(t, errors.Is(err, context.DeadlineExceeded), "error = %v", err)
			default:
				require.Nil(t, session)
				require.True(t, ringapimodels.IsConnectionError(err), "error = %v", err)
			}
		})
	}
}

func TestRecordedConnectionShutdownClosesChildBeforeSocket(t *testing.T) {
	offer, captured := recordedLiveView(t)
	closeSeen := make(chan bool, 1)
	conn := identityPeer(t, func(c *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, c, dialog, captured, false) {
			return
		}
		var frame struct {
			Method string `json:"method"`
			Dialog string `json:"dialog_id"`
		}
		if err := c.ReadJSON(&frame); err != nil {
			closeSeen <- false
			return
		}
		closeSeen <- frame.Method == "close" && frame.Dialog == dialog
		_, _, _ = c.ReadMessage()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.True(t, <-closeSeen)
	require.ErrorIs(t, session.Wait(ctx), ring.ErrSessionClosed)
	require.Equal(t, ring.SessionClosed, session.State())
	require.NoError(t, session.Close())
	require.NoError(t, conn.Close())
	_, err = conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}})
	require.True(t, ringapimodels.IsClosedError(err), "error = %v", err)
}
