package websocket_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
	ringwebsocket "github.com/portpowered/go-ring/pkg/dependencies/websocket"
	"github.com/stretchr/testify/require"
)

type continuousRPCFrame struct {
	Body continuousRPCBody `json:"body"`
}

type continuousRPCBody struct {
	Command continuousRPCCommand `json:"command"`
}

type continuousRPCCommand struct {
	Params map[string]json.RawMessage `json:"params"`
}

type stepRPCFrame struct {
	Body stepRPCBody `json:"body"`
}

type stepRPCBody struct {
	Command stepRPCCommand `json:"command"`
}

type stepRPCCommand struct {
	Method string                     `json:"method"`
	Params map[string]json.RawMessage `json:"params"`
}

type signalingReadTestError struct {
	cause error
}

type signalingWriteTestError struct {
	cause error
}

func (e signalingReadTestError) Error() string {
	return "read signaling frame: " + e.cause.Error()
}

func (e signalingReadTestError) Unwrap() error {
	return e.cause
}

func (e signalingWriteTestError) Error() string {
	return "write signaling test frame: " + e.cause.Error()
}

func (e signalingWriteTestError) Unwrap() error {
	return e.cause
}

func TestContinuousRPCWireFramePreservesExplicitZeroSpeed(t *testing.T) {
	t.Parallel()

	message := continuousRPCMessage(true)

	encoded, err := writeAndReadSignalingMessage(t, message)
	if err != nil {
		t.Fatalf("write continuous RPC failed: %v", err)
	}

	var frame continuousRPCFrame

	err = json.Unmarshal(encoded, &frame)
	if err != nil {
		t.Fatalf("decode generated continuous RPC: %v", err)
	}

	if speed := string(frame.Body.Command.Params["speed"]); speed != "0" {
		t.Fatalf("generated continuous RPC speed = %q, want explicit zero", speed)
	}
}

func TestContinuousRPCWireFrameRequiresSpeed(t *testing.T) {
	t.Parallel()

	_, err := writeAndReadSignalingMessage(t, continuousRPCMessage(false))
	if !ringerrors.IsBadRequestError(err) {
		t.Fatal("continuous RPC without speed was accepted")
	}
}

func TestStepRPCWireFramePreservesModeledCommand(t *testing.T) {
	t.Parallel()

	message := signaling.Message{
		Method:   protocol.MethodRPC,
		DialogID: "dialog-1",
		RIID:     "",
		Body: json.RawMessage(
			`{"doorbot_id":123,"session_id":"session-1","command":{"jsonrpc":"2.0","id":"command-1",` +
				`"method":"PTZ.Pan.Step","params":{"sessionId":"session-1","timestamp":1,"version":1,"direction":"LEFT"}}}`,
		),
	}

	encoded, err := writeAndReadSignalingMessage(t, message)
	if err != nil {
		t.Fatalf("write step RPC failed: %v", err)
	}

	var frame stepRPCFrame

	err = json.Unmarshal(encoded, &frame)
	require.NoError(t, err, "decode generated step RPC")
	require.Equal(t, protocol.RPCPanStep, frame.Body.Command.Method)
	require.Equal(t, `"session-1"`, string(frame.Body.Command.Params["sessionId"]))
	require.Equal(t, `"LEFT"`, string(frame.Body.Command.Params["direction"]))
	require.Equal(t, "1", string(frame.Body.Command.Params["timestamp"]))
	require.Equal(t, "1", string(frame.Body.Command.Params["version"]))
}

func TestRPCWriterRejectsMalformedAndUnsupportedCommands(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "non-object body", body: `[]`},
		{name: "missing command", body: `{}`},
		{name: "command is not object", body: `{"command":[]}`},
		{name: "missing method", body: `{"command":{}}`},
		{name: "non-string method", body: `{"command":{"method":5}}`},
		{name: "unsupported method", body: `{"command":{"method":"PTZ.Zoom"}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := writeAndReadSignalingMessage(t, signaling.Message{
				Method:   protocol.MethodRPC,
				DialogID: "dialog-1",
				RIID:     "",
				Body:     json.RawMessage(testCase.body),
			})
			if !ringerrors.IsBadRequestError(err) {
				t.Fatalf("malformed RPC error = %v, want typed bad-request error", err)
			}
		})
	}
}

func TestStreamOptionsFramePreservesAudioAndCombinedSettings(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name         string
		body         string
		audio        bool
		includeAudio bool
		wantVideo    bool
		includeVideo bool
	}{
		{
			name:         "audio only",
			body:         `{"doorbot_id":1000,"session_id":"session","audio_enabled":true}`,
			audio:        true,
			includeAudio: true,
			wantVideo:    false,
			includeVideo: false,
		},
		{
			name:         "audio and video",
			body:         `{"doorbot_id":1000,"session_id":"session","audio_enabled":true,"video_enabled":false}`,
			audio:        true,
			includeAudio: true,
			wantVideo:    false,
			includeVideo: true,
		},
		{
			name:         "video only",
			body:         `{"doorbot_id":1000,"session_id":"session","video_enabled":true}`,
			audio:        false,
			includeAudio: false,
			wantVideo:    true,
			includeVideo: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := writeAndReadSignalingMessage(t, signaling.Message{
				Method:   protocol.MethodStreamOptions,
				DialogID: "dialog-1",
				RIID:     "",
				Body:     json.RawMessage(testCase.body),
			})
			if err != nil {
				t.Fatalf("write stream options failed: %v", err)
			}

			var frame struct {
				Body map[string]json.RawMessage `json:"body"`
			}

			err = json.Unmarshal(encoded, &frame)
			require.NoError(t, err, "decode stream options frame")

			audioField, hasAudio := frame.Body["audio_enabled"]
			if hasAudio {
				require.Equal(t, testCase.audio, decodeJSONBool(t, audioField))
			}

			require.Equal(t, testCase.includeAudio, hasAudio)

			video, includesVideo := frame.Body["video_enabled"]
			require.Equal(t, testCase.includeVideo, includesVideo)

			if includesVideo {
				require.Equal(t, testCase.wantVideo, decodeJSONBool(t, video))
			}
		})
	}
}

func TestStreamOptionsRejectsEmptySetting(t *testing.T) {
	t.Parallel()

	_, err := writeAndReadSignalingMessage(t, signaling.Message{
		Method:   protocol.MethodStreamOptions,
		DialogID: "dialog-1",
		RIID:     "",
		Body:     json.RawMessage(`{"doorbot_id":1000,"session_id":"session"}`),
	})
	if !ringerrors.IsBadRequestError(err) {
		t.Fatal("stream options without audio_enabled or video_enabled were accepted")
	}
}

func decodeJSONBool(t *testing.T, encoded json.RawMessage) bool {
	t.Helper()

	var decoded bool

	err := json.Unmarshal(encoded, &decoded)
	require.NoError(t, err)

	return decoded
}

func TestReadSignalingDecodesGeneratedCloseReasonCodeUnion(t *testing.T) {
	t.Parallel()

	for name, code := range map[string]string{
		"string":  `"session_closed"`,
		"integer": `0`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			message, err := readOneSignalingFrame(t,
				`{"method":"close","dialog_id":"dialog-1","body":{"reason":{"code":`+code+`,"text":"closed"}}}`)
			if err == nil {
				t.Fatal("ReadSignaling returned without the peer closing")
			}

			if message.Method != protocol.MethodClose || message.DialogID != "dialog-1" {
				t.Fatalf("decoded close message = %#v", message)
			}

			var body map[string]json.RawMessage

			err = json.Unmarshal(message.Body, &body)
			if err != nil {
				t.Fatal(err)
			}

			var reason map[string]json.RawMessage

			err = json.Unmarshal(body["reason"], &reason)
			if err != nil {
				t.Fatal(err)
			}

			if string(reason["code"]) != code {
				t.Fatalf("close reason code = %s, want %s", reason["code"], code)
			}
		})
	}
}

func TestReadSignalingRejectsInvalidCloseReasonCode(t *testing.T) {
	t.Parallel()

	message, err := readOneSignalingFrame(t,
		`{"method":"close","dialog_id":"dialog-1","body":{"reason":{"code":true,"text":"closed"}}}`)
	if err == nil {
		t.Fatal("invalid close reason code was accepted")
	}

	if message.Method != "" {
		t.Fatalf("invalid frame reached route callback: %#v", message)
	}
}

func continuousRPCMessage(includeSpeed bool) signaling.Message {
	params := `"sessionId":"session-1","timestamp":1,"version":1,"direction":"LEFT"`
	if includeSpeed {
		params += `,"speed":0`
	}

	body := `{"doorbot_id":123,"session_id":"session-1","command":{"jsonrpc":"2.0",` +
		`"id":"command-1","method":"PTZ.Pan.Continuous","params":{` +
		params + `}}}`

	return signaling.Message{
		Method:   protocol.MethodRPC,
		DialogID: "dialog-1",
		RIID:     "",
		Body:     json.RawMessage(body),
	}
}

func writeAndReadSignalingMessage(t *testing.T, message signaling.Message) ([]byte, error) {
	t.Helper()

	frames := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		peer, err := (&gorillawebsocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}

		defer func() { _ = peer.Close() }()

		_, frame, err := peer.ReadMessage()
		if err == nil {
			frames <- frame
		}
	}))

	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	connection, response, err := gorillawebsocket.DefaultDialer.Dial(wsURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatalf("dial signaling test server: %v", err)
	}

	defer func() { _ = connection.Close() }()

	err = ringwebsocket.WriteSignaling(context.Background(), connection, message)
	if err != nil {
		return nil, signalingWriteTestError{cause: err}
	}

	select {
	case frame := <-frames:
		return frame, nil
	case <-time.After(time.Second):
		t.Fatal("signaling server did not receive a frame")

		return nil, context.DeadlineExceeded
	}
}

func readOneSignalingFrame(t *testing.T, frame string) (signaling.Message, error) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		peer, err := (&gorillawebsocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}

		defer func() { _ = peer.Close() }()

		_ = peer.WriteMessage(gorillawebsocket.TextMessage, []byte(frame))
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	connection, response, err := gorillawebsocket.DefaultDialer.Dial(wsURL, nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatalf("dial signaling test server: %v", err)
	}

	defer func() { _ = connection.Close() }()

	var message signaling.Message

	err = ringwebsocket.ReadSignaling(connection, func(decoded signaling.Message) { message = decoded })
	if err != nil {
		err = signalingReadTestError{cause: err}
	}

	return message, err
}
