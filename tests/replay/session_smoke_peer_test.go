package replay_test

import (
	"encoding/json"
	"strings"

	"github.com/gorilla/websocket"
)

type signalingSmokeWSPeer struct {
	conn                   *websocket.Conn
	dialog                 string
	lastPTZRequestObserved chan<- struct{}
}

func (peer *signalingSmokeWSPeer) serve() error {
	defer func() { _ = peer.conn.Close() }()

	err := peer.beginSession()
	if err != nil {
		return err
	}

	err = peer.serveStartupControls()
	if err != nil {
		return err
	}

	err = peer.verifyCallerControls()
	if err != nil {
		return err
	}

	err = peer.servePTZCalls()
	if err != nil {
		return err
	}

	return peer.serveClose()
}

func (peer *signalingSmokeWSPeer) beginSession() error {
	liveView, err := peer.expectMethod(liveViewMethod)
	if err != nil {
		return err
	}

	dialogID, ok := liveView["dialog_id"].(string)
	if !ok || dialogID == "" {
		return testReplayError("live_view dialog ID missing")
	}

	body, err := smokeObject(liveView, "body")
	if err != nil {
		return err
	}

	if body["doorbot_id"] != float64(1001) {
		return testReplayError("bad live_view device ID")
	}

	offer, ok := body["sdp"].(string)
	if !ok || !strings.Contains(offer, "a=mid:0") {
		return testReplayError("offer not sent")
	}

	peer.dialog = dialogID

	err = peer.write(map[string]any{
		"method":    "session_created",
		"dialog_id": peer.dialog,
		"riid":      "route-1",
		"body":      map[string]any{"doorbot_id": 1001, "session_id": "signal-1"},
	})
	if err != nil {
		return err
	}

	return peer.write(map[string]any{
		"method":    "sdp",
		"dialog_id": peer.dialog,
		"riid":      "route-1",
		"body": map[string]any{
			"doorbot_id": 1001,
			"session_id": "signal-1",
			"type":       "answer",
			"sdp":        answerSDP,
			"session_info": map[string]any{
				"session_id":    recordedControlID,
				"ping_interval": 10,
			},
		},
	})
}

func (peer *signalingSmokeWSPeer) serveStartupControls() error {
	_, err := peer.expectMethod(activateSessionMethod)
	if err != nil {
		return err
	}

	_, err = peer.expectMethod("mic_enable")
	if err != nil {
		return err
	}

	_, err = peer.expectMethod("stream_options")
	if err != nil {
		return err
	}

	return peer.write(map[string]any{
		"method":    "camera_started",
		"dialog_id": peer.dialog,
		"riid":      "route-1",
		"body":      map[string]any{"doorbot_id": 1001, "session_id": "signal-1"},
	})
}

func (peer *signalingSmokeWSPeer) verifyCallerControls() error {
	ice, err := peer.expectMethod("ice")
	if err != nil {
		return wrapReplayErrorf(err, "read trickle ICE: %v", err)
	}

	iceBody, err := smokeObject(ice, "body")
	if err != nil {
		return err
	}

	if iceBody["mid"] != "0" || iceBody["mlineindex"] != float64(0) {
		return testReplayError("expected valid trickle ICE")
	}

	for _, method := range []string{"mic_enable", "stream_options"} {
		control, readErr := peer.expectMethod(method)
		if readErr != nil {
			return wrapReplayErrorf(readErr, "read caller %s control: %v", method, readErr)
		}

		controlErr := verifySmokeControl(method, control)
		if controlErr != nil {
			return controlErr
		}
	}

	return nil
}

func verifySmokeControl(method string, control map[string]any) error {
	body, err := smokeObject(control, "body")
	if err != nil {
		return err
	}

	switch method {
	case "mic_enable":
		if body["enabled"] != false {
			return testReplayErrorf("microphone value was not forwarded: %v", body)
		}
	case "stream_options":
		if body["audio_enabled"] != false || body["video_enabled"] != true {
			return testReplayErrorf("stream options were not forwarded: %v", body)
		}
	}

	return nil
}

func (peer *signalingSmokeWSPeer) servePTZCalls() error {
	methods := []string{
		"PTZ.Pan.Step",
		"PTZ.Pan.Step",
		panContinuousMethod,
		"PTZ.Tilt.Step",
		tiltContinuousMethod,
		panContinuousMethod,
		"PTZ.Pan.Step",
		"PTZ.Tilt.Step",
	}
	for callIndex, method := range methods {
		message, err := peer.expectMethod("rpc")
		if err != nil {
			return err
		}

		callID, err := verifySmokeRPC(message, method, callIndex)
		if err != nil {
			return err
		}

		if callIndex == len(methods)-1 {
			close(peer.lastPTZRequestObserved)
		}

		replyErr := peer.replySmokeRPC(callIndex, callID)
		if replyErr != nil {
			return replyErr
		}
	}

	return nil
}

func verifySmokeRPC(message map[string]any, expected string, callIndex int) (any, error) {
	body, err := smokeObject(message, "body")
	if err != nil {
		return nil, err
	}

	if body["session_id"] != "signal-1" {
		return nil, testReplayError("outer signal session ID missing")
	}

	command, err := smokeObject(body, "command")
	if err != nil {
		return nil, err
	}

	if command["method"] != expected {
		return nil, testReplayErrorf("expected %s, got %v", expected, command["method"])
	}

	params, err := smokeObject(command, "params")
	if err != nil {
		return nil, err
	}

	if params["sessionId"] != recordedControlID {
		return nil, testReplayError("PTZ session ID domain missing")
	}

	speedErr := verifySmokeSpeed(expected, params, callIndex)
	if speedErr != nil {
		return nil, speedErr
	}

	return command["id"], nil
}

func verifySmokeSpeed(method string, params map[string]any, callIndex int) error {
	if method == panContinuousMethod {
		if callIndex == 5 {
			if params["speed"] != float64(0) {
				return testReplayErrorf("close call did not send zero pan speed: %v", params)
			}

			return nil
		}

		if params["speed"] == float64(0.5) {
			return nil
		}

		return testReplayErrorf("unexpected pan call %d method %v params %v", callIndex, method, params)
	}

	if method == tiltContinuousMethod && params["speed"] != float64(0.25) {
		return testReplayErrorf("expected tilt speed, got %v", params["speed"])
	}

	return nil
}

func (peer *signalingSmokeWSPeer) replySmokeRPC(callIndex int, callID any) error {
	switch callIndex {
	case 6:
		return peer.writeRPCReply(map[string]any{
			"jsonrpc": "2.0",
			"id":      callID,
			"error":   map[string]any{"code": 422, "message": "denied"},
		})
	case 7:
		return nil
	default:
		return peer.writeRPCReply(map[string]any{
			"jsonrpc": "2.0",
			"id":      callID,
			"result": map[string]any{
				"sessionId": recordedControlID,
				"timestamp": int64(1700000000001 + callIndex),
				"version":   1,
			},
		})
	}
}

func (peer *signalingSmokeWSPeer) serveClose() error {
	stop, err := peer.expectMethod("rpc")
	if err != nil {
		return err
	}

	body, err := smokeObject(stop, "body")
	if err != nil {
		return err
	}

	command, err := smokeObject(body, "command")
	if err != nil {
		return err
	}

	params, err := smokeObject(command, "params")
	if err != nil {
		return err
	}

	method := command["method"]
	if (method != panContinuousMethod && method != tiltContinuousMethod) || params["speed"] != float64(0) {
		return testReplayErrorf("close did not stop tracked movement: %v", command)
	}

	stopResult := map[string]any{
		"jsonrpc": "2.0",
		"id":      command["id"],
		"result": map[string]any{
			"sessionId": recordedControlID,
			"timestamp": 1700000000010,
			"version":   1,
		},
	}

	err = peer.writeRPCReply(stopResult)
	if err != nil {
		return err
	}

	_, err = peer.expectMethod("close")

	return err
}

func (peer *signalingSmokeWSPeer) writeRPCReply(command map[string]any) error {
	return peer.write(map[string]any{
		"method":    "rpc",
		"dialog_id": peer.dialog,
		"riid":      "route-1",
		"body": map[string]any{
			"doorbot_id": 1001,
			"session_id": "signal-1",
			"command":    command,
		},
	})
}

func (peer *signalingSmokeWSPeer) expectMethod(expected string) (map[string]any, error) {
	message, err := peer.read()
	if err != nil {
		return nil, err
	}

	if message["method"] != expected {
		return nil, testReplayErrorf("expected %s, got %v", expected, message["method"])
	}

	return message, nil
}

func (peer *signalingSmokeWSPeer) read() (map[string]any, error) {
	_, payload, err := peer.conn.ReadMessage()
	if err != nil {
		return nil, wrapReplayTestError("read signaling websocket frame", err)
	}

	var message map[string]any

	err = json.Unmarshal(payload, &message)
	if err != nil {
		return nil, wrapReplayTestError("decode signaling websocket frame", err)
	}

	return message, nil
}

func (peer *signalingSmokeWSPeer) write(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return wrapReplayTestError("encode signaling websocket frame", err)
	}

	return wrapReplayTestError("write signaling websocket frame", peer.conn.WriteMessage(websocket.TextMessage, payload))
}

func smokeObject(fields map[string]any, name string) (map[string]any, error) {
	value, ok := fields[name].(map[string]any)
	if !ok {
		return nil, testReplayErrorf("replay field %q has type %T, want object", name, fields[name])
	}

	return value, nil
}
