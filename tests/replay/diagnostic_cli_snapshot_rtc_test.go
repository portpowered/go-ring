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

type snapshotOfferEnvelope struct {
	Method string            `json:"method"`
	Dialog string            `json:"dialog_id"`
	Body   snapshotOfferBody `json:"body"`
}

type snapshotOfferBody struct {
	SDP string `json:"sdp"`
}

//nolint:paralleltest,funlen // LIB-05: this serial CLI-to-RTC replay keeps success and decoder-failure checks together.
func TestDiagnosticCLISnapshotFromRTCReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("builds nested CLI module")
	}

	exe := buildReplayCLI(t)
	ffmpeg := buildFakeSnapshotFFmpeg(t)
	tokenFile := filepath.Join(t.TempDir(), "tokens.json")

	tokens, err := json.Marshal(
		map[string]any{
			"access_token":  "snapshot-token",
			"refresh_token": "snapshot-refresh",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"hardware_id":   "snapshot-hardware",
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

	var legacyCalls atomic.Int32

	api := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "snapshots") {
			legacyCalls.Add(1)
		}

		switch r.URL.Path {
		case legacyClientSessionPath:
			_, _ = responseWriter.Write([]byte(`{}`))
		case clapSignalingBootstrapPath:
			_, _ = responseWriter.Write([]byte(`{"ticket":"snapshot-ticket"}`))
		default:
			http.Error(responseWriter, "unexpected HTTP request", http.StatusNotFound)
		}
	}))
	defer api.Close()

	serverErr := make(chan error, 1)

	ws := newSnapshotWebSocketPeer(t, serverErr)
	defer ws.Close()

	imageFile := filepath.Join(t.TempDir(), "snapshot.jpg")
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
		"snapshot",
		"12345",
		"--output",
		imageFile,
		"--timeout",
		"10s",
	) // #nosec G204 -- executes the CLI binary built in this test with local servers and temporary paths.

	command.Env = append(os.Environ(), "PATH="+filepath.Dir(ffmpeg)+string(os.PathListSeparator)+os.Getenv("PATH"))

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("RTC snapshot: %v\n%s", err, output)
	}

	if !strings.Contains(string(output), "image/jpeg from live view") {
		t.Fatalf("snapshot output: %s", output)
	}

	file, err := os.Open(imageFile) // #nosec G304 -- imageFile is created under this test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = file.Close() }()

	image, err := jpeg.DecodeConfig(file)
	if err != nil || image.Width != 4 || image.Height != 3 {
		t.Fatalf("snapshot JPEG: %+v, %v", image, err)
	}

	assertSnapshotServerFinished(t, serverErr)

	badImageFile := filepath.Join(t.TempDir(), "invalid.jpg")
	bad := exec.CommandContext(t.Context(),
		exe,
		"--token-file",
		tokenFile,
		"--api-base",
		api.URL,
		"--solutions-base",
		api.URL,
		"--signaling-url",
		"ws"+strings.TrimPrefix(ws.URL, "http")+"?token={token}",
		"snapshot",
		"12345",
		"--output",
		badImageFile,
		"--timeout",
		"10s",
	) // #nosec G204 -- executes the CLI binary built in this test with local servers and temporary paths.
	bad.Env = make([]string, len(command.Env)+1)
	copy(bad.Env, command.Env)
	bad.Env[len(command.Env)] = "FAKE_FFMPEG_BAD=1"

	badOutput, badErr := bad.CombinedOutput()
	if badErr == nil || !strings.Contains(string(badOutput), "did not produce a JPEG frame") {
		t.Fatalf("invalid decoder output: %v, %s", badErr, badOutput)
	}

	{
		_, err := os.Stat(badImageFile)
		if !os.IsNotExist(err) {
			t.Fatalf("invalid snapshot created a file: %v", err)
		}
	}

	assertSnapshotServerFinished(t, serverErr)

	if legacyCalls.Load() != 0 {
		t.Fatal("RTC snapshot called a legacy snapshot endpoint")
	}
}

func buildFakeSnapshotFFmpeg(t *testing.T) string {
	t.Helper()

	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	if runtime.GOOS == "windows" {
		ffmpeg += ".exe"
	}
	// A deterministic decoder stand-in verifies that actual RTP is depacketized
	// and piped to ffmpeg. The live-device check covers real H264 decoding.
	decoderSource := filepath.Join(t.TempDir(), "ffmpeg.go")

	const decoder = `package main
import("image";"image/color` +
		`";"image/jpeg";"io";"os")
func main(){ b` +
		`:=make([]byte,45);if _,err:=io.ReadFull(` +
		`os.Stdin,b);err!=nil{os.Exit(2)};if os.G` +
		`etenv("FAKE_FFMPEG_BAD")!=""{_,_=os.Stdo` +
		`ut.Write([]byte("not-jpeg"));return};m:=` +
		`image.NewRGBA(image.Rect(0,0,4,3));m.Set` +
		`(1,1,color.RGBA{R:255,A:255});if jpeg.En` +
		`code(os.Stdout,m,nil)!=nil{os.Exit(3)} }`

	err := os.WriteFile(decoderSource, []byte(decoder), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	buildDecoder := exec.CommandContext(t.Context(),
		"go",
		"build",
		"-o",
		ffmpeg,
		decoderSource,
	) // #nosec G204 -- fixed compiler and temporary test-generated source.
	{
		output, err := buildDecoder.CombinedOutput()
		if err != nil {
			t.Fatalf("build decoder stub: %v\n%s", err, output)
		}
	}

	return ffmpeg
}

//nolint:gocognit,funlen // LIB-05: one peer checks the ordered signaling and media transcript.
func newSnapshotWebSocketPeer(t *testing.T, serverErr chan<- error) *httptest.Server {
	t.Helper()

	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, request, nil)
		if err != nil {
			serverErr <- err

			return
		}

		defer func() { _ = conn.Close() }()

		var first snapshotOfferEnvelope
		{
			err := conn.ReadJSON(&first)
			if err != nil {
				serverErr <- err

				return
			}
		}

		if first.Method != liveViewMethod || first.Body.SDP == "" {
			serverErr <- cliReplayError("missing live-view SDP offer")

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
				map[string]any{"method": method, "dialog_id": first.Dialog, "riid": "route-snapshot", "body": body},
			)
		}
		{
			err := write("session_created", map[string]any{"doorbot_id": 12345, "session_id": "signal-snapshot"})
			if err != nil {
				serverErr <- err

				return
			}
		}

		{
			err := write("sdp", map[string]any{
				"doorbot_id": 12345,
				"session_id": "signal-snapshot",
				"type":       "answer",
				"sdp":        peer.LocalDescription().SDP,
				"session_info": map[string]any{
					"session_id":    "control-snapshot",
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
			err := write("camera_started", map[string]any{"doorbot_id": 12345, "session_id": "signal-snapshot"})
			if err != nil {
				serverErr <- err

				return
			}
		}

		mediaCtx, stopMedia := context.WithCancel(request.Context())
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
			{
				_, _, err := conn.ReadMessage()
				if err != nil {
					return
				}
			}
		}
	}))

	return ws
}

func assertSnapshotServerFinished(t *testing.T, serverErr <-chan error) {
	t.Helper()

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("snapshot server did not finish")
	}
}
