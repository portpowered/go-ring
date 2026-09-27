package push

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type registrationRoundTrip func(*http.Request) (*http.Response, error)

func (fn registrationRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type failedRegistrationReader struct{}

func (failedRegistrationReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRegistrationTransportTracksFailedEndpointAndRecovery(t *testing.T) {
	status := http.StatusServiceUnavailable
	transport := NewRegistrationTransport(registrationRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})).(*diagnosticTransport)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.invalid/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := transport.failed.Load(); got != "example.invalid/status" {
		t.Fatalf("failed endpoint = %v", got)
	}
	status = http.StatusOK
	response, err = transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if got := transport.failed.Load(); got != "" {
		t.Fatalf("failed endpoint after recovery = %v", got)
	}
}

func TestRegistrationBodyRejectsMalformedAndUnreadableJSON(t *testing.T) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://fcmregistrations.googleapis.com/", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	if err := omitDefaultVAPID(req); err == nil {
		t.Fatal("malformed registration JSON accepted")
	}
	req.Body = io.NopCloser(failedRegistrationReader{})
	if err := omitDefaultVAPID(req); err == nil {
		t.Fatal("unreadable registration body accepted")
	}
}
