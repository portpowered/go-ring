package replay_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/gorilla/websocket"
)

type twoSessionReplayPeer struct {
	conn    *websocket.Conn
	dialogs [2]string
}

type twoSessionRPC struct {
	dialog  string
	signal  string
	control string
	id      any
	device  int
}

func newTwoSessionReplayServer(serverErrors chan error) *httptest.Server {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	return httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(responseWriter, request, nil)
		if err != nil {
			serverErrors <- wrapReplayTestError("upgrade two-session websocket", err)

			return
		}

		peer := twoSessionReplayPeer{conn: conn, dialogs: [2]string{}}
		serverErrors <- peer.serve()
	}))
}

func (peer *twoSessionReplayPeer) serve() error {
	defer func() { _ = peer.conn.Close() }()

	err := peer.startSessions()
	if err != nil {
		return err
	}

	calls, err := peer.readRPCs()
	if err != nil {
		return err
	}

	err = peer.sendMisroutedReply(calls[0], calls[1])
	if err != nil {
		return err
	}

	err = peer.sendCallReplies(calls)
	if err != nil {
		return err
	}

	return peer.readSessionCloses()
}

func (peer *twoSessionReplayPeer) startSessions() error {
	for sessionIndex := range peer.dialogs {
		err := peer.startSession(sessionIndex)
		if err != nil {
			return err
		}
	}

	return nil
}

func (peer *twoSessionReplayPeer) startSession(sessionIndex int) error {
	liveView, err := peer.expectMethod(liveViewMethod)
	if err != nil {
		return err
	}

	dialog, ok := liveView["dialog_id"].(string)
	if !ok || dialog == "" {
		return testReplayError("live_view dialog ID missing")
	}

	peer.dialogs[sessionIndex] = dialog

	signal := fmt.Sprintf("signal-%d", sessionIndex+1)
	control := fmt.Sprintf("control-%d", sessionIndex+1)
	route := fmt.Sprintf("route-%d", sessionIndex+1)
	device := 1001 + sessionIndex

	err = peer.writeSessionCreated(dialog, route, signal, device)
	if err != nil {
		return err
	}

	err = peer.writeAnswer(dialog, route, signal, control, device)
	if err != nil {
		return err
	}

	err = peer.readSetupControls()
	if err != nil {
		return err
	}

	return peer.writeCameraStarted(dialog, route, signal, device)
}

func (peer *twoSessionReplayPeer) writeSessionCreated(dialog, route, signal string, device int) error {
	return peer.write(map[string]any{
		"method":    "session_created",
		"dialog_id": dialog,
		"riid":      route,
		"body":      map[string]any{"doorbot_id": device, "session_id": signal},
	})
}

func (peer *twoSessionReplayPeer) writeAnswer(dialog, route, signal, control string, device int) error {
	return peer.write(map[string]any{
		"method":    "sdp",
		"dialog_id": dialog,
		"riid":      route,
		"body": map[string]any{
			"doorbot_id": device,
			"session_id": signal,
			"type":       "answer",
			"sdp":        answerSDP,
			"session_info": map[string]any{
				"session_id":    control,
				"ping_interval": 10,
			},
		},
	})
}

func (peer *twoSessionReplayPeer) readSetupControls() error {
	_, err := peer.expectMethod(activateSessionMethod)
	if err != nil {
		return wrapReplayErrorf(err, "read activation: %v", err)
	}

	for _, method := range []string{"mic_enable", "stream_options"} {
		_, err = peer.expectMethod(method)
		if err != nil {
			return wrapReplayErrorf(err, "read %s: %v", method, err)
		}
	}

	return nil
}

func (peer *twoSessionReplayPeer) writeCameraStarted(dialog, route, signal string, device int) error {
	return peer.write(map[string]any{
		"method":    "camera_started",
		"dialog_id": dialog,
		"riid":      route,
		"body":      map[string]any{"doorbot_id": device, "session_id": signal},
	})
}

func (peer *twoSessionReplayPeer) readRPCs() ([2]twoSessionRPC, error) {
	var calls [2]twoSessionRPC

	for callIndex := range calls {
		message, err := peer.expectMethod("rpc")
		if err != nil {
			return calls, err
		}

		call, err := peer.parseRPC(message)
		if err != nil {
			return calls, err
		}

		calls[callIndex] = call
	}

	return calls, nil
}

func (peer *twoSessionReplayPeer) parseRPC(message map[string]any) (twoSessionRPC, error) {
	dialog, ok := message["dialog_id"].(string)
	if !ok {
		return twoSessionRPC{}, testReplayError("RPC dialog ID missing")
	}

	body, err := smokeObject(message, "body")
	if err != nil {
		return twoSessionRPC{}, err
	}

	command, err := smokeObject(body, "command")
	if err != nil {
		return twoSessionRPC{}, err
	}

	for sessionIndex, knownDialog := range peer.dialogs {
		if knownDialog == dialog {
			return twoSessionRPC{
				dialog:  dialog,
				signal:  fmt.Sprintf("signal-%d", sessionIndex+1),
				control: fmt.Sprintf("control-%d", sessionIndex+1),
				id:      command["id"],
				device:  1001 + sessionIndex,
			}, nil
		}
	}

	return twoSessionRPC{}, testReplayError("unknown session dialog")
}

func (peer *twoSessionReplayPeer) sendMisroutedReply(first, second twoSessionRPC) error {
	command := map[string]any{
		"jsonrpc": "2.0",
		"id":      first.id,
		"result":  map[string]any{"unexpected": true},
	}

	return peer.writeRPCReply(second.dialog, first, command)
}

func (peer *twoSessionReplayPeer) sendCallReplies(calls [2]twoSessionRPC) error {
	for _, call := range []twoSessionRPC{calls[1], calls[0]} {
		command := map[string]any{
			"jsonrpc": "2.0",
			"id":      call.id,
			"result": map[string]any{
				"sessionId": call.control,
				"timestamp": 1700000000001,
				"version":   1,
			},
		}

		writeErr := peer.writeRPCReply(call.dialog, call, command)
		if writeErr != nil {
			return writeErr
		}
	}

	return nil
}

func (peer *twoSessionReplayPeer) writeRPCReply(dialog string, call twoSessionRPC, command map[string]any) error {
	return peer.write(map[string]any{
		"method":    "rpc",
		"dialog_id": dialog,
		"riid":      "route-1",
		"body": map[string]any{
			"doorbot_id": call.device,
			"session_id": call.signal,
			"command":    command,
		},
	})
}

func (peer *twoSessionReplayPeer) readSessionCloses() error {
	for range 2 {
		_, err := peer.expectMethod("close")
		if err != nil {
			return wrapReplayErrorf(err, "read child close: %v", err)
		}
	}

	return nil
}

func (peer *twoSessionReplayPeer) expectMethod(expected string) (map[string]any, error) {
	message, err := peer.read()
	if err != nil {
		return nil, err
	}

	if message["method"] != expected {
		return nil, testReplayErrorf("expected %s, got %v", expected, message["method"])
	}

	return message, nil
}

func (peer *twoSessionReplayPeer) read() (map[string]any, error) {
	_, payload, err := peer.conn.ReadMessage()
	if err != nil {
		return nil, wrapReplayTestError("read two-session websocket frame", err)
	}

	var message map[string]any

	err = json.Unmarshal(payload, &message)
	if err != nil {
		return nil, wrapReplayTestError("decode two-session websocket frame", err)
	}

	return message, nil
}

func (peer *twoSessionReplayPeer) write(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return wrapReplayTestError("encode two-session websocket frame", err)
	}

	return wrapReplayTestError("write two-session websocket frame", peer.conn.WriteMessage(websocket.TextMessage, payload))
}
