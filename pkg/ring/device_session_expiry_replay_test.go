package ring

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
)

type sessionExpiryReplayAlarm struct {
	when time.Time
	ch   chan time.Time
}

type sessionExpiryReplayClock struct {
	mu     sync.Mutex
	now    time.Time
	alarms []sessionExpiryReplayAlarm
}

type sessionExpiryReplayStreamOptions struct {
	AudioEnabled bool `json:"audio_enabled"`
	VideoEnabled bool `json:"video_enabled"`
}

type sessionExpiryReplayError struct {
	detail string
	cause  error
}

type sessionExpiryReplayLiveBody struct {
	DeviceID      int                              `json:"doorbot_id"`
	SDP           string                           `json:"sdp"`
	StreamOptions sessionExpiryReplayStreamOptions `json:"stream_options"`
}

func (failure sessionExpiryReplayError) Error() string {
	if failure.cause == nil {
		return failure.detail
	}

	return failure.detail + ": " + failure.cause.Error()
}

func (failure sessionExpiryReplayError) Unwrap() error { return failure.cause }

func (clock *sessionExpiryReplayClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()

	return clock.now
}

func (clock *sessionExpiryReplayClock) After(duration time.Duration) <-chan time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()

	ch := make(chan time.Time, 1)
	clock.alarms = append(clock.alarms, sessionExpiryReplayAlarm{when: clock.now.Add(duration), ch: ch})

	return ch
}

func (clock *sessionExpiryReplayClock) fireExpiry() bool {
	clock.mu.Lock()

	if len(clock.alarms) == 0 {
		clock.mu.Unlock()

		return false
	}

	alarm := clock.alarms[0]
	clock.alarms = clock.alarms[1:]
	clock.mu.Unlock()

	alarm.ch <- alarm.when

	return true
}

func capturedSessionExpiryFrames(t *testing.T) map[string]signaling.Message {
	t.Helper()

	frames := map[string]signaling.Message{}
	for _, frame := range []struct {
		direction string
		method    string
	}{
		{direction: "client_to_server", method: protocol.MethodLiveView},
		{direction: "server_to_client", method: protocol.MethodSessionCreated},
		{direction: "server_to_client", method: protocol.MethodSDP},
		{direction: "client_to_server", method: protocol.MethodActivateSession},
		{direction: "client_to_server", method: protocol.MethodMicEnable},
		{direction: "client_to_server", method: protocol.MethodStreamOptions},
		{direction: "server_to_client", method: protocol.MethodCameraStarted},
	} {
		frames[frame.method] = capturedLiveFrame(t, frame.direction, frame.method)
	}

	return frames
}

func TestPublicSessionExpirySendsCloseAndClosesReplaySocket(t *testing.T) {
	t.Parallel()

	frames := capturedSessionExpiryFrames(t)

	var liveBody sessionExpiryReplayLiveBody

	err := json.Unmarshal(frames[protocol.MethodLiveView].Body, &liveBody)
	if err != nil {
		t.Fatal(err)
	}

	if liveBody.DeviceID <= 0 || liveBody.SDP == "" {
		t.Fatal("captured live-view request is missing its device or offer")
	}

	connection, clock, waitPeer := openSessionExpiryReplay(t, frames, int64(liveBody.DeviceID))

	startCtx, cancelStart := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStart()

	session, err := connection.StartDeviceSession(startCtx, StartDeviceSessionRequest{
		DeviceID:     "1000",
		Offer:        SessionDescription{Type: SDPTypeOffer, SDP: liveBody.SDP},
		AudioEnabled: liveBody.StreamOptions.AudioEnabled,
		VideoEnabled: liveBody.StreamOptions.VideoEnabled,
		MaxAge:       30 * time.Minute,
		ICEMode:      "",
	})
	if err != nil {
		t.Fatal(err)
	}

	if session == nil {
		t.Fatal("StartDeviceSession returned a nil session")
	}

	if !clock.fireExpiry() {
		t.Fatal("device session did not arm its maximum-age timer")
	}

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	err = session.Wait(waitCtx)

	cancelWait()

	if !errors.Is(err, signaling.ErrExpired) {
		t.Fatalf("expired session Wait = %v", err)
	}

	if session.State() != SessionExpired {
		t.Fatalf("expired session state = %s", session.State())
	}

	if !errors.Is(session.terminalError(), signaling.ErrExpired) {
		t.Fatalf("expired session terminal error = %v", session.terminalError())
	}

	err = connection.Close()
	if err != nil {
		t.Fatal(err)
	}

	peerErr := waitPeer()
	if peerErr != nil {
		t.Fatal(peerErr)
	}
}

func openSessionExpiryReplay(
	t *testing.T,
	frames map[string]signaling.Message,
	deviceID int64,
) (*SignalingConnection, *sessionExpiryReplayClock, func() error) {
	t.Helper()

	peerResult := make(chan error, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(
			w,
			r,
			nil,
		)
		if err != nil {
			peerResult <- sessionExpiryReplayError{detail: "upgrade replay websocket", cause: err}

			return
		}

		defer func() { _ = connection.Close() }()

		peerResult <- serveSessionExpiryReplay(connection, frames, deviceID)
	}))
	t.Cleanup(peer.Close)

	tickets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ticket":"synthetic-expiry"}`))
	}))
	t.Cleanup(tickets.Close)

	client, err := NewClient(
		WithHTTPClient(tickets.Client()),
		WithEndpoints(Endpoints{
			SolutionsBaseURL: tickets.URL,
			OAuthBaseURL:     "",
			APIBaseURL:       "",
			SignalingURL:     "",
		}),
		WithSignalingWebSocketURL("ws"+strings.TrimPrefix(peer.URL, "http")),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	connection, err := client.OpenSignaling(
		context.Background(),
		OpenSignalingRequest{Auth: AuthContext{AccessToken: "synthetic", HardwareID: ""}},
	)
	if err != nil {
		t.Fatal(err)
	}

	clock := &sessionExpiryReplayClock{mu: sync.Mutex{}, now: time.Unix(1700000000, 0), alarms: nil}
	connection.clock = clock

	var peerErr error

	var peerWaitOnce sync.Once

	waitPeer := func() error {
		peerWaitOnce.Do(func() {
			select {
			case peerErr = <-peerResult:
			case <-time.After(5 * time.Second):
				peerErr = sessionExpiryReplayError{detail: "expiry replay peer did not finish", cause: nil}
			}
		})

		return peerErr
	}

	t.Cleanup(func() {
		_ = connection.Close()

		peerErr := waitPeer()
		if peerErr != nil {
			t.Error(peerErr)
		}
	})

	return connection, clock, waitPeer
}

func serveSessionExpiryReplay(
	connection *websocket.Conn,
	frames map[string]signaling.Message,
	deviceID int64,
) error {
	request, err := readSessionExpiryReplayFrame(connection, "live-view request")
	if err != nil {
		return err
	}

	wantRequest := frames[protocol.MethodLiveView]
	wantRequest.DialogID = request.DialogID
	wantRequest.RIID = ""

	err = compareSessionExpiryReplayMessage(request, wantRequest)
	if err != nil {
		return err
	}

	for _, method := range []string{protocol.MethodSessionCreated, protocol.MethodSDP} {
		response := frames[method]
		response.DialogID = request.DialogID

		err = writeSessionExpiryReplayFrame(connection, response, "captured "+method+" response")
		if err != nil {
			return err
		}
	}

	for _, method := range []string{
		protocol.MethodActivateSession,
		protocol.MethodMicEnable,
		protocol.MethodStreamOptions,
	} {
		actual, err := readSessionExpiryReplayFrame(connection, "client "+method)
		if err != nil {
			return err
		}

		want := frames[method]
		want.DialogID = request.DialogID

		err = compareSessionExpiryReplayMessage(actual, want)
		if err != nil {
			return err
		}
	}

	started := frames[protocol.MethodCameraStarted]
	started.DialogID = request.DialogID

	err = writeSessionExpiryReplayFrame(connection, started, "captured camera_started response")
	if err != nil {
		return err
	}

	closeFrame, err := readSessionExpiryReplayFrame(connection, "expiry close frame")
	if err != nil {
		return err
	}

	err = validateSessionExpiryCloseFrame(closeFrame, frames, deviceID, request.DialogID)
	if err != nil {
		return err
	}

	return verifySessionExpiryReplaySocketClosed(connection)
}

func readSessionExpiryReplayFrame(connection *websocket.Conn, description string) (signaling.Message, error) {
	err := connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		return signaling.Message{}, sessionExpiryReplayError{detail: "set replay read deadline", cause: err}
	}

	var frame signaling.Message

	err = connection.ReadJSON(&frame)
	if err != nil {
		return signaling.Message{}, sessionExpiryReplayError{detail: "read " + description, cause: err}
	}

	return frame, nil
}

func writeSessionExpiryReplayFrame(connection *websocket.Conn, frame signaling.Message, description string) error {
	err := connection.WriteJSON(frame)
	if err != nil {
		return sessionExpiryReplayError{detail: "write " + description, cause: err}
	}

	return nil
}

func validateSessionExpiryCloseFrame(
	closeFrame signaling.Message,
	frames map[string]signaling.Message,
	deviceID int64,
	dialogID string,
) error {
	if closeFrame.Method != protocol.MethodClose || closeFrame.DialogID != dialogID {
		return sessionExpiryReplayError{detail: "expiry close frame method or dialog differs", cause: nil}
	}

	if closeFrame.RIID != frames[protocol.MethodSessionCreated].RIID {
		return sessionExpiryReplayError{
			detail: "expiry close frame route differs from the negotiated session",
			cause:  nil,
		}
	}

	var closeBody map[string]json.RawMessage

	err := json.Unmarshal(closeFrame.Body, &closeBody)
	if err != nil {
		return sessionExpiryReplayError{detail: "decode expiry close body", cause: err}
	}

	if len(closeBody) != 2 {
		return sessionExpiryReplayError{detail: "expiry close body has unexpected fields", cause: nil}
	}

	var gotDeviceID int64

	err = json.Unmarshal(closeBody["doorbot_id"], &gotDeviceID)
	if err != nil {
		return sessionExpiryReplayError{detail: "decode expiry close device", cause: err}
	}

	var gotSessionID string

	err = json.Unmarshal(closeBody["session_id"], &gotSessionID)
	if err != nil {
		return sessionExpiryReplayError{detail: "decode expiry close session", cause: err}
	}

	var createdBody struct {
		SessionID string `json:"session_id"`
	}

	err = json.Unmarshal(frames[protocol.MethodSessionCreated].Body, &createdBody)
	if err != nil {
		return sessionExpiryReplayError{detail: "decode captured session identity", cause: err}
	}

	if gotDeviceID != deviceID || gotSessionID != createdBody.SessionID {
		return sessionExpiryReplayError{
			detail: "expiry close body differs from the negotiated session",
			cause:  nil,
		}
	}

	return nil
}

func verifySessionExpiryReplaySocketClosed(connection *websocket.Conn) error {
	err := connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err != nil {
		return sessionExpiryReplayError{detail: "set terminal read deadline", cause: err}
	}

	_, _, err = connection.ReadMessage()
	if err == nil {
		return sessionExpiryReplayError{detail: "unexpected application frame after expiry close", cause: nil}
	}

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) || errors.Is(err, io.EOF) {
		return nil
	}

	return sessionExpiryReplayError{detail: "expiry replay socket did not close", cause: err}
}

func compareSessionExpiryReplayMessage(got, want signaling.Message) error {
	if got.Method != want.Method || got.DialogID != want.DialogID || got.RIID != want.RIID {
		return sessionExpiryReplayError{
			detail: "signaling frame envelope got " + got.Method + "/" + got.DialogID + "/" + got.RIID +
				"; want " + want.Method + "/" + want.DialogID + "/" + want.RIID,
			cause: nil,
		}
	}

	var gotBody map[string]any

	err := json.Unmarshal(got.Body, &gotBody)
	if err != nil {
		return sessionExpiryReplayError{detail: "decode actual signaling body", cause: err}
	}

	var wantBody map[string]any

	err = json.Unmarshal(want.Body, &wantBody)
	if err != nil {
		return sessionExpiryReplayError{detail: "decode expected signaling body", cause: err}
	}

	if got.Method == protocol.MethodLiveView {
		// The current generated model emits the schema's fixed offer discriminator,
		// which the historical exchange predates.
		wantBody["type"] = protocol.SDPTypeOffer
	}

	normalizeCapturedOptionalFalse(gotBody, wantBody, "enabled")

	gotOptions, gotOptionsOK := gotBody["stream_options"].(map[string]any)
	wantOptions, wantOptionsOK := wantBody["stream_options"].(map[string]any)

	if gotOptionsOK && wantOptionsOK {
		normalizeCapturedOptionalFalse(gotOptions, wantOptions, "audio_enabled")
	}

	if !reflect.DeepEqual(gotBody, wantBody) {
		for key := range gotBody {
			if !reflect.DeepEqual(gotBody[key], wantBody[key]) {
				return sessionExpiryReplayError{
					detail: "signaling frame body differs at field " + key,
					cause:  nil,
				}
			}
		}

		for key := range wantBody {
			if !reflect.DeepEqual(gotBody[key], wantBody[key]) {
				return sessionExpiryReplayError{
					detail: "signaling frame body is missing or changes field " + key,
					cause:  nil,
				}
			}
		}
	}

	return nil
}

func normalizeCapturedOptionalFalse(got, want map[string]any, key string) {
	if expected, exists := want[key]; !exists || expected != false {
		return
	}

	if actual, exists := got[key]; exists && actual != false {
		return
	}

	delete(got, key)
	delete(want, key)
}
