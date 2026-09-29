package replay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

const (
	wrongSessionCreatedDeviceScenario = "wrong session-created device"
	emptySignalingIdentityScenario    = "empty signaling identity"
	remoteCloseBeforeCameraScenario   = "remote close before camera start"
	invalidRPCBeforeCameraScenario    = "invalid RPC before camera start"
	cameraStartTimeoutScenario        = "camera start timeout"
)

func TestRecordedSessionNegotiationFailureVariants(t *testing.T) {
	t.Parallel()

	offer, recorded := recordedLiveView(t)

	for _, scenario := range []string{
		wrongSessionCreatedDeviceScenario, emptySignalingIdentityScenario, "wrong SDP device",
		"mismatched signaling identity", "missing control identity", "same control and signaling identity",
		"invalid answer SDP", "remote close before answer", remoteCloseBeforeCameraScenario,
		invalidRPCBeforeCameraScenario, cameraStartTimeoutScenario, "socket closes before answer",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()

			conn := identityPeer(t, func(connection *websocket.Conn, dialog string) {
				serveRecordedNegotiationFailure(t, connection, dialog, recorded, scenario)
			})

			t.Cleanup(func() { _ = conn.Close() })

			wait := 2 * time.Second
			if scenario == cameraStartTimeoutScenario {
				wait = 150 * time.Millisecond
			}

			ctx, cancel := context.WithTimeout(context.Background(), wait)
			t.Cleanup(cancel)

			_, err := conn.StartDeviceSession(
				ctx,
				ring.StartDeviceSessionRequest{
					DeviceID: "1001",
					Offer:    ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
					ICEMode:  ring.ICETrickle,
				},
			)
			if err == nil {
				t.Fatal("invalid recorded negotiation variant accepted")
			}
		})
	}
}

func serveRecordedNegotiationFailure(
	t *testing.T,
	connection *websocket.Conn,
	dialog string,
	recorded map[string]json.RawMessage,
	scenario string,
) {
	t.Helper()

	created := recordedSessionFrame(t, recorded["session_created"], dialog)
	answer := recordedSessionFrame(t, recorded["sdp"], dialog)
	createdBody := replayObjectField(t, created, "body")
	mutateRecordedNegotiationFrames(t, created, answer, scenario)

	if scenario == "remote close before answer" {
		_ = connection.WriteJSON(map[string]any{"method": "close", "dialog_id": dialog, "body": createdBody})

		return
	}

	if !writeRecordedNegotiationFrame(t, connection, created) || shouldStopAfterSessionCreated(scenario) {
		return
	}

	if !writeRecordedNegotiationFrame(t, connection, answer) || !isCameraStartFailureScenario(scenario) {
		return
	}

	if !readActivation(connection) {
		t.Error("activation missing before camera-start failure")

		return
	}

	serveCameraStartFailure(connection, dialog, createdBody, scenario)
}

func mutateRecordedNegotiationFrames(
	t *testing.T,
	created, answer map[string]any,
	scenario string,
) {
	t.Helper()

	createdBody := replayObjectField(t, created, "body")
	answerBody := replayObjectField(t, answer, "body")
	info := replayObjectField(t, answerBody, "session_info")

	switch scenario {
	case wrongSessionCreatedDeviceScenario:
		createdBody["doorbot_id"] = float64(999)
	case emptySignalingIdentityScenario:
		createdBody["session_id"] = ""
	case "wrong SDP device":
		answerBody["doorbot_id"] = float64(999)
	case "mismatched signaling identity":
		answerBody["session_id"] = "another-session"
	case "missing control identity":
		info["session_id"] = ""
	case "same control and signaling identity":
		info["session_id"] = createdBody["session_id"]
	case "invalid answer SDP":
		answerBody["sdp"] = "not SDP"
	}
}

func writeRecordedNegotiationFrame(t *testing.T, connection *websocket.Conn, frame map[string]any) bool {
	t.Helper()

	err := connection.WriteJSON(frame)
	if err != nil {
		t.Error(err)

		return false
	}

	return true
}

func shouldStopAfterSessionCreated(scenario string) bool {
	return scenario == "socket closes before answer" || scenario == wrongSessionCreatedDeviceScenario ||
		scenario == emptySignalingIdentityScenario
}

func isCameraStartFailureScenario(scenario string) bool {
	return scenario == remoteCloseBeforeCameraScenario || scenario == invalidRPCBeforeCameraScenario ||
		scenario == cameraStartTimeoutScenario
}

func serveCameraStartFailure(
	connection *websocket.Conn,
	dialog string,
	createdBody map[string]any,
	scenario string,
) {
	switch scenario {
	case remoteCloseBeforeCameraScenario:
		_ = connection.WriteJSON(map[string]any{"method": "close", "dialog_id": dialog, "body": createdBody})
	case invalidRPCBeforeCameraScenario:
		_ = connection.WriteJSON(map[string]any{
			"method":    "rpc",
			"dialog_id": dialog,
			"body": map[string]any{
				"doorbot_id": 1001,
				"session_id": createdBody["session_id"],
				"command":    "bad",
			},
		})
	case cameraStartTimeoutScenario:
		_, _, _ = connection.ReadMessage()
	}
}

func TestRecordedSessionRejectsInvalidStartPolicy(t *testing.T) {
	t.Parallel()

	offer, _ := recordedLiveView(t)
	for _, tc := range []ring.StartDeviceSessionRequest{
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, ICEMode: "unknown"},
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, MaxAge: -time.Second},
		{DeviceID: "1001", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer}, MaxAge: 61 * time.Minute},
	} {
		conn := identityPeer(t, func(c *websocket.Conn, _ string) { _, _, _ = c.ReadMessage() })
		{
			_, err := conn.StartDeviceSession(context.Background(), tc)
			if err == nil {
				t.Fatalf("invalid session policy accepted: %+v", tc)
			}
		}

		_ = conn.Close()
	}
}
