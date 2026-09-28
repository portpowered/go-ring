package replay_test

import (
	"context"
	"encoding/json"
	"io/fs"
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
func openProductionTranscript(t *testing.T, file string) (*ring.SignalingConnection, *replay.WebSocketServer, *replay.Transport) {
	t.Helper()
	data, err := fs.ReadFile(os.DirFS(filepath.Join("fixtures", "signaling", "synthetic", "paired")), file)
	require.NoError(t, err)
	var transcript struct {
		Provenance string             `json:"provenance"`
		Handshake  replay.WSHandshake `json:"handshake"`
		Steps      []replay.WSStep    `json:"steps"`
	}
	require.NoError(t, json.Unmarshal(data, &transcript))
	require.Contains(t, transcript.Provenance, "synthetic")
	require.NotEmpty(t, transcript.Steps)
	peer := replay.NewWebSocketServer(transcript.Steps, 2*time.Second, transcript.Handshake)
	t.Cleanup(peer.Close)
	ticket, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "legacy-ticket.json"))
	require.NoError(t, err)
	transport := replay.NewTransport(ticket)
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: ticket.Request.Origin}),
		ring.WithSignalingWebSocketURL(peer.URL()+"/ws?token={token}"),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	conn, err := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return conn, peer, transport
}

func TestProductionPushSignalingTranscript(t *testing.T) {
	conn, peer, transport := openProductionTranscript(t, "push-production.json")
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

func TestProductionPlaybackSignalingTranscript(t *testing.T) {
	conn, peer, transport := openProductionTranscript(t, "playback-production.json")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := conn.StartPlayback(ctx, ring.StartPlaybackRequest{DeviceID: "1000", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP}})
	require.NoError(t, err)
	require.Equal(t, answerSDP, session.Answer().SDP)
	require.NoError(t, session.SendICE(ctx, ring.ICECandidateRequest{Candidate: "candidate:synthetic", MLineIndex: 0}))
	event, err := session.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "notification", event.Method)
	require.Contains(t, string(event.Body), "accepted")
	// The playback heartbeat is a real one-second timer owned by the session.
	require.NoError(t, peer.WaitStep(5, 2*time.Second))
	require.NoError(t, session.Close())
	require.NoError(t, peer.AssertComplete(3*time.Second))
	require.NoError(t, transport.AssertConsumed())
}

func TestProductionLiveSignalingTranscript(t *testing.T) {
	conn, peer, transport := openProductionTranscript(t, "live-production.json")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: "1000", Offer: ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP}, VideoEnabled: true, ICEMode: ring.ICETrickle})
	if err != nil {
		t.Fatalf("start device session: %v; replay: %v", err, peer.AssertComplete(time.Second))
	}
	require.Equal(t, answerSDP, session.Answer().SDP)
	require.NoError(t, session.SendICE(ctx, ring.ICECandidateRequest{Candidate: "candidate:synthetic", MID: "0", MLineIndex: 0}))
	result, err := session.PanStep(ctx, ring.PanStepRequest{Direction: ring.PanRight})
	require.NoError(t, err)
	require.Equal(t, "synthetic-control", result.SessionID)
	require.NoError(t, session.Close())
	require.NoError(t, peer.AssertComplete(3*time.Second))
	require.NoError(t, transport.AssertConsumed())
}

// Every supported AsyncAPI channel must be exercised by a stored transcript
// that a production client/session test consumes. Helper-only frames have no
// channel because they are absent from the checked-in AsyncAPI surface.
func TestProductionSignalingChannelInventory(t *testing.T) {
	doc := loadYAML(t, filepath.Join("..", "..", "api", "asyncapi.yaml"))
	channels := object(doc["channels"])
	actions := map[string]string{}
	for _, raw := range object(doc["operations"]) {
		operation := object(raw)
		ref, _ := object(operation["channel"])["$ref"].(string)
		if ref == "" {
			continue
		}
		kind := "expect"
		if operation["action"] == "receive" {
			kind = "send"
		}
		actions[strings.TrimPrefix(ref, "#/channels/")] = kind
	}
	seen := map[string]bool{}
	for _, name := range []string{"push-production.json", "push-heartbeat-production.json", "playback-production.json", "live-production.json"} {
		data, err := fs.ReadFile(os.DirFS(filepath.Join("fixtures", "signaling", "synthetic", "paired")), name)
		require.NoError(t, err)
		var transcript struct {
			Steps []replay.WSStep `json:"steps"`
		}
		require.NoError(t, json.Unmarshal(data, &transcript))
		require.NotEmpty(t, transcript.Steps)
		for _, step := range transcript.Steps {
			if step.Channel == "" {
				continue
			}
			require.Contains(t, channels, step.Channel)
			require.Equal(t, actions[step.Channel], step.Kind, "%s in %s", step.Channel, name)
			require.True(t, step.Template, "%s must match the complete frame", step.Channel)
			seen[step.Channel] = true
		}
	}
	for channel := range channels {
		require.True(t, seen[channel], "missing production transcript for %s", channel)
	}
}
