package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/stretchr/testify/require"
)

// The stored exchange is driven by the public client and push subscription.
// Dialog and request IDs are UUIDs bound on the first outbound frame and
// checked on subsequent outbound and inbound frames.
func TestProductionPushSignalingTranscript(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("fixtures", "signaling", "synthetic", "paired", "push-production.json"))
	require.NoError(t, err)
	var transcript struct {
		Provenance string             `json:"provenance"`
		Handshake  replay.WSHandshake `json:"handshake"`
		Steps      []replay.WSStep    `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(data, &transcript))
	require.Contains(t, transcript.Provenance, "synthetic")
	require.Len(t, transcript.Steps, 4)
	peer := replay.NewWebSocketServer(transcript.Steps, 2*time.Second, transcript.Handshake)
	defer peer.Close()
	ticket, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "legacy-ticket.json"))
	require.NoError(t, err)
	transport := replay.NewTransport(ticket)
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: ticket.Request.Origin}),
		ring.WithSignalingWebSocketURL(peer.URL()+"/ws?token={token}"),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()
	conn, err := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}})
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	filter := ring.PushFilter{FilterIdentifier: "synthetic-filter", NotificationScope: "event", NotificationType: "shoulder_tap", Filters: ring.PushFilters{DoorbotIDs: []int64{1000}}}
	subscription, err := conn.SubscribePush(ctx, []ring.PushFilter{filter})
	require.NoError(t, err)
	event, err := subscription.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "shoulder_tap", event.NotificationType)
	require.Equal(t, "synthetic-subscription", event.SubscriptionID)
	require.True(t, strings.Contains(string(event.Payload), "synthetic-location"))
	require.NoError(t, subscription.Close())
	require.NoError(t, peer.AssertComplete(3*time.Second))
	require.NoError(t, transport.AssertConsumed())
}
