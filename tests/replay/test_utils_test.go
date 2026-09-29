package replay_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/mocks"
	"github.com/portpowered/go-ring/pkg/ring"
)

type replayTestError struct {
	message   string
	operation string
	cause     error
}

func (failure replayTestError) Error() string {
	if failure.message != "" {
		return failure.message
	}

	return failure.operation + ": " + failure.cause.Error()
}

func (failure replayTestError) Unwrap() error { return failure.cause }

func testReplayError(message string) error {
	return replayTestError{message: message, operation: "", cause: nil}
}

func testReplayErrorf(format string, args ...any) error {
	return testReplayError(fmt.Sprintf(format, args...))
}

func wrapReplayErrorf(cause error, format string, args ...any) error {
	return replayTestError{message: fmt.Sprintf(format, args...), operation: "", cause: cause}
}

const (
	capturedClientToServerDirection = "client_to_server"
	capturedServerToClientDirection = "server_to_client"
	syntheticOAuthArgumentsPage     = `<script id="oauth-args">{"csrf-` + `token":"fixture-csrf"}</script>`
	syntheticEmailTwoFactorState    = `{"tsv_state":"email"}`
	capturedDeviceDetailFixture     = "device-detail"
	capturedDeviceTimelineFixture   = "device-timeline"
	capturedDeviceRebootFixture     = "device-reboot"
	capturedBootstrapTicketFixture  = "bootstrap-ticket"
	fakeVideoPayload                = "fake video data"
	legacyClientSessionPath         = "/clients_api/session"
	legacyDeviceListPath            = "/device_info/v3/devices"
	oauthAuthorizePath              = "/oauth/v2/authorize"
	oauthSignInPath                 = "/oauth/v2/signin"
	oauthTwoFactorPath              = "/oauth/v2/2fa/verify"
	// #nosec G101 -- this is a public OAuth route, not credential data.
	oauthTokenPath              = "/oauth/token"
	clapSignalingBootstrapPath  = "/api/v1/clap/ticket/request/signalsocket"
	liveViewMethod              = "live_view"
	playbackWireToken           = "playback"
	playbackTimelineEntryPoint  = "timeline"
	shoulderTapNotificationType = "shoulder_tap"
)

func wrapReplayTestError(operation string, cause error) error {
	if cause == nil {
		return nil
	}

	return replayTestError{message: "", operation: operation, cause: cause}
}

func TestWrapReplayTestErrorPreservesCause(t *testing.T) {
	t.Parallel()

	wrapped := wrapReplayTestError("run replay operation", io.EOF)
	if !errors.Is(wrapped, io.EOF) {
		t.Fatalf("wrapped error %v does not preserve EOF", wrapped)
	}

	if wrapReplayTestError("run replay operation", nil) != nil {
		t.Fatal("wrapping nil returned a non-nil error")
	}
}

// getFixtureDir returns the path to the test fixtures directory.
func getFixtureDir() string {
	programCounter, filename, line, _ := runtime.Caller(0)
	if programCounter == 0 || filename == "" || line == 0 {
		panic("could not locate replay test fixtures")
	}

	return filepath.Join(filepath.Dir(filename), "fixtures", "http", "baseline")
}

// newTestClient creates a new Ring client with a mock HTTP transport.
func newTestClient() (*ring.Client, *mocks.MockTransport) {
	fixtureDir := getFixtureDir()
	mockTransport := mocks.NewMockTransport(fixtureDir)
	httpClient := &http.Client{
		Transport: mockTransport,
	}

	client, err := ring.NewClient(
		ring.WithHTTPClient(httpClient),
	)
	if err != nil {
		panic(err)
	}

	return client, mockTransport
}

// newTestClientWithMockTransport creates a Ring client with a mock transport.
func newTestClientWithMockTransport() (*ring.Client, *mocks.MockTransport) {
	fixtureDir := getFixtureDir()
	mockTransport := mocks.NewMockTransport(fixtureDir)
	httpClient := &http.Client{
		Transport: mockTransport,
	}

	client, err := ring.NewClient(
		ring.WithHTTPClient(httpClient),
	)
	if err != nil {
		panic(err)
	}

	return client, mockTransport
}

// newTestContext creates a test context.
func newTestContext() context.Context {
	return context.Background()
}
