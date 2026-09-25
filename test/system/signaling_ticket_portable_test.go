package system_test

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
	x, err := replay.LoadExchange(filepath.Join("..", "porting-fixtures", "legacy-ticket.json"))
	if err != nil {
		t.Fatal(err)
	}
	transport := replay.NewTransport(x)
	peer := replay.NewWebSocketServer(nil, time.Second)
	defer peer.Close()
	dialer := &captureDialer{delegate: websocket.DefaultDialer}
	client, err := ring.NewClient(
		ring.WithAccessToken("portable-token"),
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: x.Request.Origin}),
		ring.WithSignalingWebSocketURL(peer.URL()+"?token={token}"),
		ring.WithWebSocketDialer(dialer),
	)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if !strings.Contains(dialer.url, "token=synthetic-legacy-ticket") {
		t.Fatalf("fixture ticket missing from websocket URL: %s", dialer.url)
	}
	if err := transport.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
	if err := peer.AssertComplete(time.Second); err != nil {
		t.Fatal(err)
	}
}
