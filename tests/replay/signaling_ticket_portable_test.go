package replay_test

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
)

// The same synthetic POST ticket exchange is used by the Python harness.
// It covers the supported legacy profile; the captured C1 GET is distinct.
func TestPortableLegacyTicketBootstrap(t *testing.T) {
	t.Parallel()

	exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "legacy-ticket.json"))
	if err != nil {
		t.Fatal(err)
	}

	transport := replay.NewTransport(exchange)

	peer := replay.NewWebSocketServer(
		nil,
		time.Second,
		replay.WSHandshake{Path: "/", Query: map[string][]string{"token": {"synthetic-legacy-ticket"}}},
	)
	t.Cleanup(peer.Close)

	dialer := &captureDialer{delegate: websocket.DefaultDialer}

	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: exchange.Request.Origin}),
		ring.WithSignalingWebSocketURL(peer.URL()+"?token={token}"),
		ring.WithWebSocketDialer(dialer),
	)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := client.OpenSignaling(
		context.Background(),
		ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "portable-token", HardwareID: ""}},
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	_ = conn.Close()

	if !strings.Contains(dialer.url, "token=synthetic-legacy-ticket") {
		t.Fatalf("fixture ticket missing from websocket URL: %s", dialer.url)
	}

	{
		err := transport.AssertConsumed()
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := peer.AssertComplete(time.Second)
		if err != nil {
			t.Fatal(err)
		}
	}
}
