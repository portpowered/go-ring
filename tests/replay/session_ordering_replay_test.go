package replay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

const (
	prematureCameraStartScenario        = "premature-camera-start"
	conflictingCreatedScenario          = "conflicting-created"
	cameraStartBeforeActivationScenario = "camera-start-before-activation-read"
	matchingDuplicateCreatedScenario    = "matching-duplicate-created"
)

func TestRecordedLiveSessionFrameOrdering(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)

	for _, scenario := range []string{
		"answer-before-created",
		conflictingCreatedScenario,
		prematureCameraStartScenario,
		cameraStartBeforeActivationScenario,
		matchingDuplicateCreatedScenario,
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
				serveRecordedFrameOrdering(t, connection, dialog, captured, scenario)
			})

			t.Cleanup(func() { _ = conn.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			t.Cleanup(cancel)

			session, err := conn.StartDeviceSession(
				ctx,
				ring.StartDeviceSessionRequest{
					DeviceID: "1001",
					Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
				},
			)

			assertRecordedFrameOrderingResult(t, scenario, session, err)
		})
	}
}

func serveRecordedFrameOrdering(
	t *testing.T,
	connection *websocket.Conn,
	dialog string,
	captured map[string]json.RawMessage,
	scenario string,
) {
	t.Helper()

	created := recordedSessionFrame(t, captured["session_created"], dialog)
	answer := recordedSessionFrame(t, captured["sdp"], dialog)
	camera := recordedSessionFrame(t, captured["camera_started"], dialog)

	if serveOutOfOrderInitialFrames(connection, created, answer, camera, scenario) {
		return
	}

	if !writeFrameIfConnected(connection, created) {
		return
	}

	if !writeFrameOrderingDuplicate(t, connection, captured, dialog, scenario) {
		return
	}

	if !writeFrameIfConnected(connection, answer) || scenario == conflictingCreatedScenario {
		return
	}

	if scenario == cameraStartBeforeActivationScenario && !writeFrameIfConnected(connection, camera) {
		return
	}

	if !readActivation(connection) {
		return
	}

	if scenario == matchingDuplicateCreatedScenario {
		_ = connection.WriteJSON(camera)
	}

	_, _, err := connection.ReadMessage()
	if err != nil {
		return
	}
}

func serveOutOfOrderInitialFrames(
	connection *websocket.Conn,
	created, answer, camera map[string]any,
	scenario string,
) bool {
	switch scenario {
	case "answer-before-created":
		_ = connection.WriteJSON(answer)
		_ = connection.WriteJSON(created)

		return true
	case prematureCameraStartScenario:
		_ = connection.WriteJSON(camera)
	}

	return false
}

func writeFrameOrderingDuplicate(
	t *testing.T,
	connection *websocket.Conn,
	captured map[string]json.RawMessage,
	dialog, scenario string,
) bool {
	t.Helper()

	if scenario != conflictingCreatedScenario && scenario != matchingDuplicateCreatedScenario {
		return true
	}

	duplicate := recordedSessionFrame(t, captured["session_created"], dialog)
	if scenario == conflictingCreatedScenario {
		body := replayObjectField(t, duplicate, "body")
		body["session_id"] = "other-session"
	}

	return writeFrameIfConnected(connection, duplicate)
}

func writeFrameIfConnected(connection *websocket.Conn, frame map[string]any) bool {
	return connection.WriteJSON(frame) == nil
}

func assertRecordedFrameOrderingResult(
	t *testing.T,
	scenario string,
	session *ring.DeviceSession,
	err error,
) {
	t.Helper()

	switch scenario {
	case cameraStartBeforeActivationScenario, matchingDuplicateCreatedScenario:
		require.NoError(t, err)
		require.Equal(t, ring.SessionActive, session.State())
		require.NoError(t, session.Close())
	case prematureCameraStartScenario:
		require.Nil(t, session)
		require.ErrorIs(t, err, context.DeadlineExceeded, "error = %v", err)
	default:
		require.Nil(t, session)
		require.True(t, ringapimodels.IsConnectionError(err), "error = %v", err)
	}
}

func TestRecordedConnectionShutdownClosesChildBeforeSocket(t *testing.T) {
	t.Parallel()

	offer, captured := recordedLiveView(t)
	closeSeen := make(chan bool, 1)
	conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
		if !serveRecordedNegotiation(t, connection, dialog, captured, false) {
			return
		}

		var frame struct {
			Method string `json:"method"`
			Dialog string `json:"dialog_id"`
		}

		err := connection.ReadJSON(&frame)
		if err != nil {
			closeSeen <- false

			return
		}

		closeSeen <- frame.Method == "close" && frame.Dialog == dialog

		_, _, _ = connection.ReadMessage()
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	session, err := conn.StartDeviceSession(
		ctx,
		ring.StartDeviceSessionRequest{
			DeviceID: "1001",
			Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
		},
	)
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.True(t, <-closeSeen)
	require.ErrorIs(t, session.Wait(ctx), ring.ErrSessionClosed)
	require.Equal(t, ring.SessionClosed, session.State())
	require.NoError(t, session.Close())
	require.NoError(t, conn.Close())
	_, err = conn.StartDeviceSession(
		ctx,
		ring.StartDeviceSessionRequest{
			DeviceID: "1001",
			Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
		},
	)
	require.True(t, ringapimodels.IsClosedError(err), "error = %v", err)
}
