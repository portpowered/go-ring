package ring_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

type captureSignalingDialerError string

func (failure captureSignalingDialerError) Error() string { return string(failure) }

type captureSignalingDialer struct {
	url string
}

func (d *captureSignalingDialer) DialContext(
	_ context.Context,
	url string,
	_ http.Header,
) (*websocket.Conn, *http.Response, error) {
	d.url = url

	return nil, nil, captureSignalingDialerError("stop after capturing signaling URL")
}

func TestRTCWebSocketURLExplicitOverrideHasStablePrecedence(t *testing.T) {
	t.Parallel()

	const explicitURL = "ws://explicit.example/ws"

	for _, explicitFirst := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
			responseWriter.Header().Set("Content-Type", "application/json")
			_, _ = responseWriter.Write([]byte(`{"ticket":"synthetic-ticket"}`))
		}))
		t.Cleanup(server.Close)

		dialer := &captureSignalingDialer{url: ""}
		endpointsOption := ring.WithEndpoints(ring.Endpoints{
			SolutionsBaseURL: server.URL,
			SignalingURL:     "wss://profile.example/ws",
		})
		overrideOption := ring.WithSignalingWebSocketURL(explicitURL)

		clientOptions := []ring.Option{ring.WithHTTPClient(server.Client())}
		if explicitFirst {
			clientOptions = append(clientOptions, overrideOption, endpointsOption)
		} else {
			clientOptions = append(clientOptions, endpointsOption, overrideOption)
		}

		clientOptions = append(clientOptions, ring.WithWebSocketDialer(dialer))

		client, err := ring.NewClient(clientOptions...)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = client.Close() })

		_, err = client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{
			Auth: ring.AuthContext{AccessToken: "test-token", HardwareID: ""},
		})
		if err == nil {
			t.Fatal("signaling dial unexpectedly succeeded")
		}

		if dialer.url != explicitURL {
			t.Fatalf("signaling URL = %q, want explicit override %q", dialer.url, explicitURL)
		}
	}
}
