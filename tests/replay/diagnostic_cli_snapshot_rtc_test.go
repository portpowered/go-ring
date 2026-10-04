package replay_test

import (
	"encoding/json"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

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

	api := newDiagnosticCLIHTTPPairs(t, "cli-snapshot-bootstrap.json")

	ws, rtc := newDiagnosticCLIRTCWebSocketPeer(t, "cli-snapshot.json")

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
		ws.URL()+"?token={token}",
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

	err = ws.AssertComplete(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}

	err = rtc.assertClosed()
	if err != nil {
		t.Fatal(err)
	}

	badImageFile := filepath.Join(t.TempDir(), "invalid.jpg")
	badWS, badRTC := newDiagnosticCLIRTCWebSocketPeer(t, "cli-snapshot.json")
	bad := exec.CommandContext(t.Context(),
		exe,
		"--token-file",
		tokenFile,
		"--api-base",
		api.URL,
		"--solutions-base",
		api.URL,
		"--signaling-url",
		badWS.URL()+"?token={token}",
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

	err = badWS.AssertComplete(10 * time.Second)
	if err != nil {
		t.Fatal(err)
	}

	err = badRTC.assertClosed()
	if err != nil {
		t.Fatal(err)
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
