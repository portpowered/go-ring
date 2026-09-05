package unit

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/test/mocks"
)

// getFixtureDir returns the path to the test fixtures directory
func getFixtureDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "fixtures")
}

// newTestClient creates a new Ring client with a mock HTTP transport
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

// newTestClientWithToken creates a new Ring client with a token and mock transport
func newTestClientWithToken(token string) (*ring.Client, *mocks.MockTransport) {
	fixtureDir := getFixtureDir()
	mockTransport := mocks.NewMockTransport(fixtureDir)
	httpClient := &http.Client{
		Transport: mockTransport,
	}

	client, err := ring.NewClient(
		ring.WithAccessToken(token),
		ring.WithHTTPClient(httpClient),
	)
	if err != nil {
		panic(err)
	}

	return client, mockTransport
}

// newTestContext creates a test context
func newTestContext() context.Context {
	return context.Background()
}

// newTestClientWithRTCWebSocketURL creates a new Ring client with a token, mock transport, and custom RTC websocket URL
func newTestClientWithRTCWebSocketURL(token string, wsURL string) (*ring.Client, *mocks.MockTransport) {
	fixtureDir := getFixtureDir()
	mockTransport := mocks.NewMockTransport(fixtureDir)
	httpClient := &http.Client{
		Transport: mockTransport,
	}

	client, err := ring.NewClient(
		ring.WithAccessToken(token),
		ring.WithHTTPClient(httpClient),
		ring.WithRTCWebSocketURL(wsURL),
	)
	if err != nil {
		panic(err)
	}

	return client, mockTransport
}
