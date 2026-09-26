package replay_test

import (
	"context"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
)

func TestDiagnosticCLISnapshotFromRTCReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}
	cliDir, err := filepath.Abs(filepath.Join("..", "..", "cmd", "go-ring"))
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "go-ring")
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if runtime.GOOS == "windows" {
		exe += ".exe"
		ffmpeg += ".exe"
	}
	build := exec.Command("go", "build", "-o", exe, ".")
	build.Dir, build.Env = cliDir, append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	// A deterministic decoder stand-in verifies that actual RTP is depacketized
	// and piped to ffmpeg. The live-device check covers real H264 decoding.
	decoderSource := filepath.Join(t.TempDir(), "ffmpeg.go")
	const decoder = `package main
import("image";"image/color";"image/jpeg";"io";"os")
func main(){ b:=make([]byte,45);if _,err:=io.ReadFull(os.Stdin,b);err!=nil{os.Exit(2)};if os.Getenv("FAKE_FFMPEG_BAD")!=""{_,_=os.Stdout.Write([]byte("not-jpeg"));return};m:=image.NewRGBA(image.Rect(0,0,4,3));m.Set(1,1,color.RGBA{R:255,A:255});if jpeg.Encode(os.Stdout,m,nil)!=nil{os.Exit(3)} }`
	if err := os.WriteFile(decoderSource, []byte(decoder), 0o600); err != nil {
		t.Fatal(err)
	}
	buildDecoder := exec.Command("go", "build", "-o", ffmpeg, decoderSource)
	if output, err := buildDecoder.CombinedOutput(); err != nil {
		t.Fatalf("build decoder stub: %v\n%s", err, output)
	}
	tokenFile := filepath.Join(t.TempDir(), "tokens.json")
	tokens, _ := json.Marshal(map[string]any{"access_token": "snapshot-token", "refresh_token": "snapshot-refresh", "token_type": "Bearer", "expires_in": 3600, "hardware_id": "snapshot-hardware", "received_at": time.Now()})
	if err := os.WriteFile(tokenFile, tokens, 0o600); err != nil {
		t.Fatal(err)
	}
	var legacyCalls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "snapshots") {
			legacyCalls.Add(1)
		}
		switch r.URL.Path {
		case "/clients_api/session":
			_, _ = w.Write([]byte(`{}`))
		case "/api/v1/clap/ticket/request/signalsocket":
			_, _ = w.Write([]byte(`{"ticket":"snapshot-ticket"}`))
		default:
			http.Error(w, "unexpected HTTP request", http.StatusNotFound)
		}
	}))
	defer api.Close()
	serverErr := make(chan error, 1)
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		var first struct {
			Method string `json:"method"`
			Dialog string `json:"dialog_id"`
			Body   struct {
				SDP string `json:"sdp"`
			} `json:"body"`
		}
		if err := conn.ReadJSON(&first); err != nil {
			serverErr <- err
			return
		}
		if first.Method != "live_view" || first.Body.SDP == "" {
			serverErr <- fmt.Errorf("missing live-view SDP offer")
			return
		}
		peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			serverErr <- err
			return
		}
		defer peer.Close()
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
			return conn.WriteJSON(map[string]any{"method": method, "dialog_id": first.Dialog, "riid": "route-snapshot", "body": body})
		}
		if err := write("session_created", map[string]any{"doorbot_id": 12345, "session_id": "signal-snapshot"}); err != nil {
			serverErr <- err
			return
		}
		if err := write("sdp", map[string]any{"doorbot_id": 12345, "session_id": "signal-snapshot", "type": "answer", "sdp": peer.LocalDescription().SDP, "session_info": map[string]any{"session_id": "control-snapshot", "ping_interval": 10}}); err != nil {
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
		if err := write("camera_started", map[string]any{"doorbot_id": 12345, "session_id": "signal-snapshot"}); err != nil {
			serverErr <- err
			return
		}
		mediaCtx, stopMedia := context.WithCancel(context.Background())
		defer stopMedia()
		go func() {
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-mediaCtx.Done():
					return
				case <-tick.C:
					_ = track.WriteSample(media.Sample{Data: []byte{0x10, 0x00, 0x00}, Duration: 20 * time.Millisecond})
				}
			}
		}()
		serverErr <- nil
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer ws.Close()
	imageFile := filepath.Join(t.TempDir(), "snapshot.jpg")
	command := exec.Command(exe, "--token-file", tokenFile, "--api-base", api.URL, "--solutions-base", api.URL, "--signaling-url", "ws"+strings.TrimPrefix(ws.URL, "http")+"?token={token}", "snapshot", "12345", "--output", imageFile, "--timeout", "10s")
	command.Env = append(os.Environ(), "PATH="+filepath.Dir(ffmpeg)+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("RTC snapshot: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "image/jpeg from live view") {
		t.Fatalf("snapshot output: %s", output)
	}
	file, err := os.Open(imageFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	image, err := jpeg.DecodeConfig(file)
	if err != nil || image.Width != 4 || image.Height != 3 {
		t.Fatalf("snapshot JPEG: %+v, %v", image, err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot server did not finish")
	}
	badImageFile := filepath.Join(t.TempDir(), "invalid.jpg")
	bad := exec.Command(exe, "--token-file", tokenFile, "--api-base", api.URL, "--solutions-base", api.URL, "--signaling-url", "ws"+strings.TrimPrefix(ws.URL, "http")+"?token={token}", "snapshot", "12345", "--output", badImageFile, "--timeout", "10s")
	bad.Env = append(command.Env, "FAKE_FFMPEG_BAD=1")
	badOutput, badErr := bad.CombinedOutput()
	if badErr == nil || !strings.Contains(string(badOutput), "did not produce a JPEG frame") {
		t.Fatalf("invalid decoder output: %v, %s", badErr, badOutput)
	}
	if _, err := os.Stat(badImageFile); !os.IsNotExist(err) {
		t.Fatalf("invalid snapshot created a file: %v", err)
	}
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("invalid snapshot server did not finish")
	}
	if legacyCalls.Load() != 0 {
		t.Fatal("RTC snapshot called a legacy snapshot endpoint")
	}
}
