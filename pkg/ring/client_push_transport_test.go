package ring_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/pkg/ring"
)

type fcmRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper fcmRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestFCMHTTPTransportOptionReachesBuiltInReceiver(t *testing.T) {
	t.Parallel()

	requests := make(chan string, 1)
	transport := fcmRoundTripper(func(request *http.Request) (*http.Response, error) {
		select {
		case requests <- request.URL.Host:
		default:
		}

		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("{}")),
			Request:    request,
		}, nil
	})

	client, err := ring.NewClient(ring.WithFCMHTTPTransport(transport))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	connection, err := client.ConnectPush(ctx, ring.ConnectPushRequest{
		Auth:        ring.AuthContext{AccessToken: "test-token", HardwareID: ""},
		Credentials: nil,
		DeviceIDs:   nil,
		Ding:        false,
		Motion:      false,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = connection.Close() }()

	select {
	case host := <-requests:
		if host == "" {
			t.Fatal("FCM transport received a request without a host")
		}
	case <-ctx.Done():
		t.Fatal("built-in FCM receiver did not use the configured HTTP transport")
	}
}

func TestFCMHTTPTransportOptionRejectsNil(t *testing.T) {
	t.Parallel()

	_, err := ring.NewClient(ring.WithFCMHTTPTransport(nil))
	if err == nil {
		t.Fatal("nil FCM HTTP transport was accepted")
	}
}
