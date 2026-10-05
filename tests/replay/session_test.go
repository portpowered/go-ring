package replay_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
)

const (
	activateSessionMethod = "activate_session"
	recordedControlID     = "control-1"
)

// Cross-feature smoke; focused captured and portable replay cases live in
// session_portable_replay_test.go and internal/signaling/recording_test.go.
func TestSignalingSessionIntegrationSmoke(t *testing.T) {
	t.Parallel()

	var httpCalls int

	httpPeer := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		httpCalls++

		if request.Method != http.MethodPost || request.URL.Path != clapSignalingBootstrapPath {
			t.Errorf("unexpected bootstrap request %s %s", request.Method, request.URL.Path)
		}

		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authorization")
		}

		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"ticket":"synthetic-ticket"}`))
	}))
	defer httpPeer.Close()

	serverErrors := make(chan error, 1)
	lastPTZRequestObserved := make(chan struct{})

	wsPeer := newSignalingSmokeWSPeer(t, serverErrors, lastPTZRequestObserved)
	defer wsPeer.Close()

	wsURL := "ws" + strings.TrimPrefix(wsPeer.URL, "http") + "?token={token}"
	dialer := &captureDialer{delegate: websocket.DefaultDialer}

	client, err := ring.NewClient(
		ring.WithHTTPClient(httpPeer.Client()),
		ring.WithEndpoints(ring.Endpoints{
			SolutionsBaseURL: httpPeer.URL,
			OAuthBaseURL:     "",
			APIBaseURL:       "",
			SignalingURL:     "",
		}),
		ring.WithSignalingWebSocketURL(wsURL),
		ring.WithWebSocketDialer(dialer),
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

	if !strings.Contains(dialer.url, "token=synthetic-ticket") || dialer.headers.Get("User-Agent") == "" {
		t.Fatalf("injected dialer missed endpoint or headers: %s", dialer.url)
	}

	session, err := conn.StartDeviceSession(
		context.Background(),
		ring.StartDeviceSessionRequest{
			DeviceID:     "1001",
			Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offerSDP},
			AudioEnabled: false,
			VideoEnabled: true,
			MaxAge:       0,
			ICEMode:      ring.ICETrickle,
		},
	)
	if err != nil {
		failSmokeSessionStart(t, err, serverErrors)
	}

	assertSmokeAnswer(t, session)
	exerciseSmokeSessionControls(t, session)
	runSmokePTZCalls(t, session, lastPTZRequestObserved)
	closeSmokeSession(t, conn, session)

	if httpCalls != 1 {
		t.Fatalf("ticket endpoint called %d times", httpCalls)
	}

	assertSmokePeerCompleted(t, serverErrors)
}

func assertSmokeAnswer(t *testing.T, session *ring.DeviceSession) {
	t.Helper()

	answer := session.Answer()
	if answer.Type != ring.SDPTypeAnswer || !strings.Contains(answer.SDP, "a=sendonly") {
		t.Fatalf("invalid answer: %+v", answer)
	}
}

func exerciseSmokeSessionControls(t *testing.T, session *ring.DeviceSession) {
	t.Helper()

	invalidCandidates := []ring.ICECandidateRequest{
		{Candidate: "candidate:x", MID: "unknown", MLineIndex: 0},
		{Candidate: "candidate:x", MID: "0", MLineIndex: 1},
	}
	for _, candidate := range invalidCandidates {
		err := session.SendICE(context.Background(), candidate)
		if err == nil {
			t.Fatalf("accepted candidate with mismatched media identity: %+v", candidate)
		}
	}

	err := session.SendICE(context.Background(), ring.ICECandidateRequest{
		Candidate: "candidate:1 1 UDP 1 127.0.0.1 9 typ host", MID: "0", MLineIndex: 0,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = session.SetMicrophone(context.Background(), ring.SetMicrophoneRequest{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}

	audio, video := false, true

	err = session.SetStreamOptions(context.Background(), ring.SetStreamOptionsRequest{
		AudioEnabled: &audio, VideoEnabled: &video,
	})
	if err != nil {
		t.Fatal(err)
	}
}

type smokePTZCall struct {
	run     func(context.Context) (*ring.PTZResult, error)
	outcome int
}

func runSmokePTZCalls(t *testing.T, session *ring.DeviceSession, lastPTZRequestObserved <-chan struct{}) {
	t.Helper()

	calls := []smokePTZCall{
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.PanStep(ctx, ring.PanStepRequest{Direction: "RIGHT"})
		}, outcome: 0},
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.PanStep(ctx, ring.PanStepRequest{Direction: "LEFT"})
		}, outcome: 0},
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: "RIGHT", Speed: 0.5})
		}, outcome: 0},
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.TiltStep(ctx, ring.TiltStepRequest{Direction: "DOWN"})
		}, outcome: 0},
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.TiltContinuous(ctx, ring.TiltContinuousRequest{Direction: "UP", Speed: 0.25})
		}, outcome: 0},
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.StopPTZ(ctx, ring.StopPTZRequest{Axis: ring.PanAxis})
		}, outcome: 0},
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.PanStep(ctx, ring.PanStepRequest{Direction: "RIGHT"})
		}, outcome: 1},
		{run: func(ctx context.Context) (*ring.PTZResult, error) {
			return session.TiltStep(ctx, ring.TiltStepRequest{Direction: "UP"})
		}, outcome: 2},
	}
	for _, call := range calls {
		assertSmokePTZCall(t, call, lastPTZRequestObserved)
	}
}

func assertSmokePTZCall(
	t *testing.T,
	call smokePTZCall,
	lastPTZRequestObserved <-chan struct{},
) {
	t.Helper()

	if call.outcome == 2 {
		assertDeadlineSmokePTZCall(t, call, lastPTZRequestObserved)

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	result, callErr := call.run(ctx)

	cancel()

	switch call.outcome {
	case 1:
		var rpcErr *ring.RPCError
		if !errors.As(callErr, &rpcErr) || rpcErr.Code != 422 {
			t.Fatalf("RPC error = %v", callErr)
		}
	default:
		assertSuccessfulSmokePTZResult(t, result, callErr)
	}
}

func assertDeadlineSmokePTZCall(
	t *testing.T,
	call smokePTZCall,
	lastPTZRequestObserved <-chan struct{},
) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	callDone := make(chan error, 1)

	go func() {
		_, err := call.run(ctx)
		callDone <- err
	}()

	select {
	case <-lastPTZRequestObserved:
	case <-time.After(time.Second):
		t.Fatal("peer did not observe PTZ request before deadline")
	}

	select {
	case callErr := <-callDone:
		if !errors.Is(callErr, context.DeadlineExceeded) {
			t.Fatalf("deadline PTZ error = %v", callErr)
		}
	case <-time.After(time.Second):
		t.Fatal("PTZ request did not return after deadline")
	}
}

func assertSuccessfulSmokePTZResult(t *testing.T, result *ring.PTZResult, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}

	if len(result.Raw) == 0 {
		t.Fatal("empty PTZ result")
	}

	if result.SessionID != recordedControlID || result.Timestamp == 0 || result.Version != 1 {
		t.Fatalf("typed acknowledgement not populated: %+v", result)
	}
}

func closeSmokeSession(t *testing.T, conn *ring.SignalingConnection, session *ring.DeviceSession) {
	t.Helper()

	err := conn.Close()
	if err != nil {
		t.Fatal(err)
	}

	err = session.Close()
	if err != nil {
		t.Fatalf("child close after parent teardown should be harmless: %v", err)
	}
}

func assertSmokePeerCompleted(t *testing.T, serverErrors <-chan error) {
	t.Helper()

	select {
	case err := <-serverErrors:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("local signaling peer did not complete")
	}
}

func failSmokeSessionStart(t *testing.T, startErr error, serverErrors <-chan error) {
	t.Helper()

	select {
	case peerErr := <-serverErrors:
		if peerErr != nil {
			t.Fatalf("session start failed: %v; local peer: %v", startErr, peerErr)
		}
	case <-time.After(time.Second):
		t.Fatal(startErr)
	}
}
func newSignalingSmokeWSPeer(
	t *testing.T,
	serverErrors chan error,
	lastPTZRequestObserved chan<- struct{},
) *httptest.Server {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	return httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("token") != "synthetic-ticket" {
			serverErrors <- testReplayError("ticket was not placed in signaling URL query")

			return
		}

		conn, err := upgrader.Upgrade(responseWriter, request, nil)
		if err != nil {
			serverErrors <- wrapReplayTestError("upgrade signaling websocket", err)

			return
		}

		peer := signalingSmokeWSPeer{
			conn:                   conn,
			dialog:                 "",
			lastPTZRequestObserved: lastPTZRequestObserved,
		}
		serverErrors <- peer.serve()
	}))
}

const offerSDP = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\n" +
	"s=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\n" +
	"c=IN IP4 0.0.0.0\r\na=mid:0\r\na=recvonly\r\n"

const answerSDP = "v=0\r\no=- 2 2 IN IP4 127.0.0.1\r\n" +
	"s=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\n" +
	"c=IN IP4 0.0.0.0\r\na=mid:0\r\na=sendonly\r\n"

type captureDialer struct {
	delegate *websocket.Dialer
	url      string
	headers  http.Header
}

func (d *captureDialer) DialContext(
	ctx context.Context,
	url string,
	h http.Header,
) (*websocket.Conn, *http.Response, error) {
	d.url = url
	d.headers = h.Clone()

	conn, response, err := d.delegate.DialContext(ctx, url, h)

	return conn, response, wrapReplayTestError("dial captured signaling websocket", err)
}
