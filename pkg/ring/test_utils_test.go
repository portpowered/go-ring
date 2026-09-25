package ring_test

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"

	"github.com/portpowered/go-ring/internal/testkit/mocks"
	"github.com/portpowered/go-ring/pkg/ring"
)

// getFixtureDir returns the path to the test fixtures directory
func getFixtureDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "tests", "replay", "fixtures", "legacy")
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
