package replay_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/pkg/ring"
)

func TestTwoSessionsRouteRepliesByDialog(t *testing.T) {
	t.Parallel()

	tickets := newSessionTicketsServer()
	defer tickets.Close()

	serverErr := make(chan error, 1)

	websocketPeer := newTwoSessionReplayServer(serverErr)
	defer websocketPeer.Close()

	conn := openTwoSessionConnection(t, tickets, websocketPeer)
	first := startRoutedSession(t, conn, "1001", ring.ICETrickle)
	second := startRoutedSession(t, conn, "1002", ring.ICENonTrickle)
	assertNonTrickleRejectsICE(t, second)
	assertRoutedCallsComplete(t, first, second)
	closeRoutedSessions(t, conn, first, second)
	awaitTwoSessionPeer(t, serverErr)
}

func newSessionTicketsServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"ticket":"fixture"}`))
	}))
}

func openTwoSessionConnection(
	t *testing.T,
	tickets *httptest.Server,
	websocketPeer *httptest.Server,
) *ring.SignalingConnection {
	t.Helper()

	websocketURL := "ws" + strings.TrimPrefix(websocketPeer.URL, "http")

	client, err := ring.NewClient(
		ring.WithHTTPClient(tickets.Client()),
		ring.WithEndpoints(ring.Endpoints{
			SolutionsBaseURL: tickets.URL,
			OAuthBaseURL:     "",
			APIBaseURL:       "",
			SignalingURL:     "",
		}),
		ring.WithSignalingWebSocketURL(websocketURL),
	)
	if err != nil {
		t.Fatal(err)
	}

	conn, err := client.OpenSignaling(
		context.Background(),
		ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "test-token", HardwareID: ""}},
	)
	if err != nil {
		t.Fatal(err)
	}

	return conn
}

func startRoutedSession(
	t *testing.T,
	conn *ring.SignalingConnection,
	deviceID string,
	iceMode ring.ICECandidateMode,
) *ring.DeviceSession {
	t.Helper()

	session, err := conn.StartDeviceSession(
		context.Background(),
		ring.StartDeviceSessionRequest{
			DeviceID:     deviceID,
			Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
			AudioEnabled: false,
			VideoEnabled: true,
			MaxAge:       0,
			ICEMode:      iceMode,
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	return session
}

func assertNonTrickleRejectsICE(t *testing.T, session *ring.DeviceSession) {
	t.Helper()

	err := session.SendICE(
		context.Background(),
		ring.ICECandidateRequest{
			Candidate:  "candidate:2 1 UDP 1 127.0.0.1 9 typ host",
			MID:        "0",
			MLineIndex: 0,
		},
	)
	if err == nil {
		t.Fatal("non-trickle session accepted a candidate send")
	}
}

func assertRoutedCallsComplete(t *testing.T, first, second *ring.DeviceSession) {
	t.Helper()

	type rpcResult struct {
		result *ring.PTZResult
		err    error
	}

	results := make(chan rpcResult, 2)

	go func() {
		value, err := first.PanStep(context.Background(), ring.PanStepRequest{Direction: "LEFT"})
		results <- rpcResult{result: value, err: err}
	}()

	go func() {
		value, err := second.TiltStep(context.Background(), ring.TiltStepRequest{Direction: "DOWN"})
		results <- rpcResult{result: value, err: err}
	}()

	for range 2 {
		select {
		case result := <-results:
			if result.err != nil || result.result == nil {
				t.Fatalf("session RPC failed: %v", result.err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("routed RPC did not complete")
		}
	}
}

func closeRoutedSessions(
	t *testing.T,
	conn *ring.SignalingConnection,
	first, second *ring.DeviceSession,
) {
	t.Helper()

	for _, session := range []*ring.DeviceSession{first, second} {
		err := session.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	connErr := conn.Close()
	if connErr != nil {
		t.Fatal(connErr)
	}
}

func awaitTwoSessionPeer(t *testing.T, serverErr <-chan error) {
	t.Helper()

	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("two-session peer did not complete")
	}
}
