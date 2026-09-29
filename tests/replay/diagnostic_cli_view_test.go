package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

type viewOfferEnvelope struct {
	Method string        `json:"method"`
	Dialog string        `json:"dialog_id"`
	Body   viewOfferBody `json:"body"`
}

type viewOfferBody struct {
	SDP string `json:"sdp"`
}

type viewRPCEnvelope struct {
	Method string      `json:"method"`
	Body   viewRPCBody `json:"body"`
}

type viewRPCBody struct {
	Command viewRPCCommand `json:"command"`
}

type viewRPCCommand struct {
	ID     string        `json:"id"`
	Method string        `json:"method"`
	Params viewRPCParams `json:"params"`
}

type viewRPCParams struct {
	Direction string `json:"direction"`
}

//nolint:paralleltest // LIB-05: this timed CLI-to-WebSocket transcript runs serially.
func TestDiagnosticCLIViewAndArrowReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}

	exe := buildReplayCLI(t)
	tokenFile := filepath.Join(t.TempDir(), "tokens.json")

	tokens, err := json.Marshal(
		map[string]any{
			"access_token":  "view-token",
			"refresh_token": "view-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"hardware_id":   "view-hardware",
			"received_at":   time.Now(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	{
		err := os.WriteFile(tokenFile, tokens, 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	api := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path == legacyClientSessionPath {
			_, _ = responseWriter.Write([]byte(`{}`))

			return
		}

		if request.URL.Path == clapSignalingBootstrapPath {
			_, _ = responseWriter.Write([]byte(`{"ticket":"view-ticket"}`))

			return
		}

		http.Error(responseWriter, "unexpected HTTP request", http.StatusNotFound)
	}))
	defer api.Close()

	serverErr := make(chan error, 1)

	ws := newViewWebSocketPeer(serverErr)
	defer ws.Close()

	command := exec.CommandContext(t.Context(),
		exe,
		"--token-file",
		tokenFile,
		"--api-base",
		api.URL,
		"--solutions-base",
		api.URL,
		"--signaling-url",
		"ws"+strings.TrimPrefix(ws.URL, "http")+"?token={token}",
		"view",
		"12345",
		"--player",
		"none",
		"--debug",
	) // #nosec G204 -- executes the CLI binary built in this test with local servers and temporary token.
	keys, keyWriter := io.Pipe()
	command.Stdin = keys

	go func() {
		_, _ = keyWriter.Write([]byte("\x1b[C\x1b[D\x1b[A\x1b[B"))

		time.Sleep(time.Second)

		_, _ = keyWriter.Write([]byte("q"))
		_ = keyWriter.Close()
	}()

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI view: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "Session active") ||
		!strings.Contains(string(output), "First video packet received") ||
		!strings.Contains(string(output), "PTZ command acknowledged") ||
		!strings.Contains(string(output), "Remote SDP answer applied") {
		t.Fatalf("view output: %s", output)
	}

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("view server did not finish")
	}
}

//nolint:gocognit,funlen // LIB-05: one peer checks the ordered signaling, media, and PTZ transcript.
func newViewWebSocketPeer(serverErr chan<- error) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, request, nil)
		if err != nil {
			serverErr <- err

			return
		}

		defer func() { _ = conn.Close() }()

		if request.URL.Query().Get("token") != "view-ticket" {
			serverErr <- cliReplayError("missing ticket")

			return
		}

		var first viewOfferEnvelope
		{
			err := conn.ReadJSON(&first)
			if err != nil {
				serverErr <- err

				return
			}
		}

		if first.Method != liveViewMethod {
			serverErr <- cliReplayError("first method " + first.Method)

			return
		}

		peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			serverErr <- err

			return
		}

		defer func() { _ = peer.Close() }()

		track, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
			"video",
			"camera",
		)
		if err != nil {
			serverErr <- err

			return
		}

		{
			_, err := peer.AddTrack(track)
			if err != nil {
				serverErr <- err

				return
			}
		}

		{
			err := peer.SetRemoteDescription(webrtc.SessionDescription{
				Type: webrtc.SDPTypeOffer,
				SDP:  first.Body.SDP,
			})
			if err != nil {
				serverErr <- err

				return
			}
		}

		answer, err := peer.CreateAnswer(nil)
		if err != nil {
			serverErr <- err

			return
		}

		complete := webrtc.GatheringCompletePromise(peer)

		{
			err := peer.SetLocalDescription(answer)
			if err != nil {
				serverErr <- err

				return
			}
		}

		select {
		case <-complete:
		case <-time.After(10 * time.Second):
			serverErr <- cliReplayError("server ICE gathering timeout")

			return
		}

		write := func(method string, body map[string]any) error {
			return conn.WriteJSON(
				map[string]any{"method": method, "dialog_id": first.Dialog, "riid": "route-view", "body": body},
			)
		}
		{
			err := write("session_created", map[string]any{"doorbot_id": 12345, "session_id": "signal-view"})
			if err != nil {
				serverErr <- err

				return
			}
		}

		{
			err := write("sdp", map[string]any{
				"doorbot_id": 12345,
				"session_id": "signal-view",
				"type":       "answer",
				"sdp":        peer.LocalDescription().SDP,
				"session_info": map[string]any{
					"session_id":    "control-view",
					"ping_interval": 10,
				},
			})
			if err != nil {
				serverErr <- err

				return
			}
		}

		for _, expected := range []string{"activate_session", "mic_enable", "stream_options"} {
			var message struct {
				Method string `json:"method"`
			}

			err := conn.ReadJSON(&message)
			if err != nil {
				serverErr <- err

				return
			}

			if message.Method != expected {
				serverErr <- cliReplayError(fmt.Sprintf("expected %s, got %s", expected, message.Method))

				return
			}
		}

		{
			err := write("camera_started", map[string]any{"doorbot_id": 12345, "session_id": "signal-view"})
			if err != nil {
				serverErr <- err

				return
			}
		}

		mediaCtx, stopMedia := context.WithCancel(request.Context())
		defer stopMedia()

		go func() {
			tick := time.NewTicker(30 * time.Millisecond)
			defer tick.Stop()

			for {
				select {
				case <-mediaCtx.Done():
					return
				case <-tick.C:
					_ = track.WriteSample(media.Sample{Data: []byte{0x10, 0x00, 0x00}, Duration: 30 * time.Millisecond})
				}
			}
		}()

		err = replayViewArrowRPCs(conn, write)
		if err != nil {
			serverErr <- err

			return
		}

		serverErr <- nil

		for {
			{
				_, _, err := conn.ReadMessage()
				if err != nil {
					return
				}
			}
		}
	}))
}

func replayViewArrowRPCs(conn *websocket.Conn, write func(string, map[string]any) error) error {
	for _, expected := range []struct{ method, direction string }{
		{"PTZ.Pan.Step", "RIGHT"},
		{"PTZ.Pan.Step", "LEFT"},
		{"PTZ.Tilt.Step", "UP"},
		{"PTZ.Tilt.Step", "DOWN"},
	} {
		var command viewRPCEnvelope

		err := conn.ReadJSON(&command)
		if err != nil {
			return errors.Join(cliReplayError("read arrow RPC"), err)
		}

		if command.Method != "rpc" || command.Body.Command.Method != expected.method ||
			command.Body.Command.Params.Direction != expected.direction {
			return cliReplayError(fmt.Sprintf("wrong arrow RPC: %+v", command))
		}

		reply := map[string]any{
			"jsonrpc": "2.0",
			"id":      command.Body.Command.ID,
			"result":  map[string]any{"sessionId": "control-view", "timestamp": 1700000000000, "version": 1},
		}

		var envelope any = reply

		if expected.direction == "LEFT" {
			envelope = map[string]any{"destination": "client", "protocol": "jsonrpc", "message": reply}
		}

		err = write("rpc", map[string]any{"doorbot_id": 12345, "session_id": "signal-view", "command": envelope})
		if err != nil {
			return err
		}
	}

	return nil
}
