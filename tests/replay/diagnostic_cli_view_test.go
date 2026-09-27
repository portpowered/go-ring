package replay_test

import (
	"context"
	"encoding/json"
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

func TestDiagnosticCLIViewAndArrowReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}
	exe := buildReplayCLI(t)
	tokenFile := filepath.Join(t.TempDir(), "tokens.json")
	tokens, err := json.Marshal(map[string]any{"access_token": "view-token", "refresh_token": "view-refresh", "token_type": "Bearer", "expires_in": 3600, "hardware_id": "view-hardware", "received_at": time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, tokens, 0o600); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/clients_api/session" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.URL.Path == "/api/v1/clap/ticket/request/signalsocket" {
			_, _ = w.Write([]byte(`{"ticket":"view-ticket"}`))
			return
		}
		http.Error(w, "unexpected HTTP request", http.StatusNotFound)
	}))
	defer api.Close()
	serverErr := make(chan error, 1)
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.Close() }()
		if r.URL.Query().Get("token") != "view-ticket" {
			serverErr <- fmt.Errorf("missing ticket")
			return
		}
		var first viewOfferEnvelope
		if err := conn.ReadJSON(&first); err != nil {
			serverErr <- err
			return
		}
		if first.Method != "live_view" {
			serverErr <- fmt.Errorf("first method %s", first.Method)
			return
		}
		peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = peer.Close() }()
		track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8}, "video", "camera")
		if err != nil {
			serverErr <- err
			return
		}
		if _, err := peer.AddTrack(track); err != nil {
			serverErr <- err
			return
		}
		if err := peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: first.Body.SDP}); err != nil {
			serverErr <- err
			return
		}
		answer, err := peer.CreateAnswer(nil)
		if err != nil {
			serverErr <- err
			return
		}
		complete := webrtc.GatheringCompletePromise(peer)
		if err := peer.SetLocalDescription(answer); err != nil {
			serverErr <- err
			return
		}
		select {
		case <-complete:
		case <-time.After(10 * time.Second):
			serverErr <- fmt.Errorf("server ICE gathering timeout")
			return
		}
		write := func(method string, body map[string]any) error {
			return conn.WriteJSON(map[string]any{"method": method, "dialog_id": first.Dialog, "riid": "route-view", "body": body})
		}
		if err := write("session_created", map[string]any{"doorbot_id": 12345, "session_id": "signal-view"}); err != nil {
			serverErr <- err
			return
		}
		if err := write("sdp", map[string]any{"doorbot_id": 12345, "session_id": "signal-view", "type": "answer", "sdp": peer.LocalDescription().SDP, "session_info": map[string]any{"session_id": "control-view", "ping_interval": 10}}); err != nil {
			serverErr <- err
			return
		}
		for _, expected := range []string{"activate_session", "mic_enable", "stream_options"} {
			var message struct {
				Method string `json:"method"`
			}
			if err := conn.ReadJSON(&message); err != nil {
				serverErr <- err
				return
			}
			if message.Method != expected {
				serverErr <- fmt.Errorf("expected %s, got %s", expected, message.Method)
				return
			}
		}
		if err := write("camera_started", map[string]any{"doorbot_id": 12345, "session_id": "signal-view"}); err != nil {
			serverErr <- err
			return
		}
		mediaCtx, stopMedia := context.WithCancel(context.Background())
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
		for _, expected := range []struct{ method, direction string }{{"PTZ.Pan.Step", "RIGHT"}, {"PTZ.Pan.Step", "LEFT"}, {"PTZ.Tilt.Step", "UP"}, {"PTZ.Tilt.Step", "DOWN"}} {
			var command viewRPCEnvelope
			if err := conn.ReadJSON(&command); err != nil {
				serverErr <- err
				return
			}
			if command.Method != "rpc" || command.Body.Command.Method != expected.method || command.Body.Command.Params.Direction != expected.direction {
				serverErr <- fmt.Errorf("wrong arrow RPC: %+v", command)
				return
			}
			reply := map[string]any{"jsonrpc": "2.0", "id": command.Body.Command.ID, "result": map[string]any{"sessionId": "control-view", "timestamp": 1700000000000, "version": 1}}
			var envelope any = reply
			if expected.direction == "LEFT" {
				envelope = map[string]any{"destination": "client", "protocol": "jsonrpc", "message": reply}
			}
			if err := write("rpc", map[string]any{"doorbot_id": 12345, "session_id": "signal-view", "command": envelope}); err != nil {
				serverErr <- err
				return
			}
		}
		serverErr <- nil
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ws.Close()
	command := exec.Command(exe, "--token-file", tokenFile, "--api-base", api.URL, "--solutions-base", api.URL, "--signaling-url", "ws"+strings.TrimPrefix(ws.URL, "http")+"?token={token}", "view", "12345", "--player", "none", "--debug") // #nosec G204 -- executes the CLI binary built in this test with local servers and temporary token.
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
	if !strings.Contains(string(output), "Session active") || !strings.Contains(string(output), "First video packet received") || !strings.Contains(string(output), "PTZ command acknowledged") || !strings.Contains(string(output), "Remote SDP answer applied") {
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
