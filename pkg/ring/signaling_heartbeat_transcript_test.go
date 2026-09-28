package ring

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/stretchr/testify/require"
)

func TestProductionPushHeartbeatTranscript(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "replay", "fixtures", "signaling", "synthetic", "paired", "push-heartbeat-production.json"))
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
	ticket, err := replay.LoadExchange(filepath.Join("..", "..", "tests", "replay", "fixtures", "http", "synthetic", "legacy-ticket.json"))
	require.NoError(t, err)
	transport := replay.NewTransport(ticket)
	client, err := NewClient(
		WithHTTPClient(&http.Client{Transport: transport}),
		WithEndpoints(Endpoints{SolutionsBaseURL: ticket.Request.Origin}),
		WithSignalingWebSocketURL(peer.URL()+"/ws?token={token}"),
	)
	require.NoError(t, err)
	defer func() { require.NoError(t, client.Close()) }()
	conn, err := client.OpenSignaling(context.Background(), OpenSignalingRequest{Auth: AuthContext{AccessToken: "portable-token"}})
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	filter := PushFilter{FilterIdentifier: "synthetic-filter", NotificationScope: "event", NotificationType: "shoulder_tap", Filters: PushFilters{DoorbotIDs: []int64{1000}}}
	subscription, err := conn.SubscribePush(ctx, []PushFilter{filter})
	require.NoError(t, err)
	heartbeatDone := make(chan struct{})
	go func() {
		subscription.heartbeatAt(ctx, 100*time.Millisecond)
		close(heartbeatDone)
	}()
	require.NoError(t, peer.WaitStep(2, time.Second))
	require.NoError(t, subscription.Close())
	<-heartbeatDone
	require.NoError(t, peer.AssertComplete(3*time.Second))
	require.NoError(t, transport.AssertConsumed())
}
