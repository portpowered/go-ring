package replay_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/portpowered/go-ring/internal/testkit/replay"
)

const (
	cliRTCNegotiationTimeout = 10 * time.Second
	cliRTCMediaInterval      = 20 * time.Millisecond
	cliRTCMediaCloseTimeout  = 2 * time.Second
)

type cliRTCTranscript struct {
	Provenance string             `json:"provenance"`
	Handshake  replay.WSHandshake `json:"handshake"`
	Steps      []replay.WSStep    `json:"steps"`
}

type cliRTCPeerSession struct {
	mu        sync.Mutex
	peer      *webrtc.PeerConnection
	cancel    context.CancelFunc
	mediaDone chan struct{}
	closed    bool

	closeOnce sync.Once
	closeErr  error
}

type cliRTCReplayError struct {
	reason string
	cause  error
}

func (err cliRTCReplayError) Error() string {
	if err.cause != nil {
		return err.reason + ": " + err.cause.Error()
	}

	return err.reason
}

func (err cliRTCReplayError) Unwrap() error { return err.cause }

func newDiagnosticCLIRTCWebSocketPeer(
	t *testing.T,
	fixture string,
) (*replay.WebSocketServer, *cliRTCPeerSession) {
	t.Helper()

	transcript := loadDiagnosticCLIRTCTranscript(t, fixture)
	peerSession := new(cliRTCPeerSession)
	bindDiagnosticCLIRTCSteps(t, fixture, transcript.Steps, peerSession)

	// Close the replay server before the peer state so interrupted reads stop
	// before test cleanup waits for the media goroutine and WebRTC peer.
	t.Cleanup(func() {
		err := peerSession.close()
		if err != nil {
			t.Errorf("close synthetic RTC peer: %v", err)
		}
	})

	server := replay.NewWebSocketServer(transcript.Steps, cliRTCNegotiationTimeout, transcript.Handshake)
	t.Cleanup(server.Close)

	return server, peerSession
}

func loadDiagnosticCLIRTCTranscript(t *testing.T, fixture string) cliRTCTranscript {
	t.Helper()

	if fixture != "cli-view.json" && fixture != "cli-snapshot.json" {
		t.Fatalf("unknown CLI signaling transcript %q", fixture)
	}

	data, err := os.ReadFile(
		filepath.Join("fixtures", "signaling", "synthetic", "paired", fixture),
	) // #nosec G304 -- fixture names are fixed test cases.
	if err != nil {
		t.Fatalf("read signaling transcript %s: %v", fixture, err)
	}

	var transcript cliRTCTranscript

	err = json.Unmarshal(data, &transcript)
	if err != nil {
		t.Fatalf("decode signaling transcript %s: %v", fixture, err)
	}

	if !strings.Contains(strings.ToLower(transcript.Provenance), "synthetic") {
		t.Fatalf("signaling transcript %s is not labeled synthetic", fixture)
	}

	if len(transcript.Steps) < 2 || transcript.Steps[0].Kind != "expect" ||
		transcript.Steps[0].Frame != "text" || !transcript.Steps[0].Template ||
		transcript.Steps[len(transcript.Steps)-1].Kind != "expect" {
		t.Fatalf("signaling transcript %s has no strict offer-to-close sequence", fixture)
	}

	if !transcript.Handshake.ExactHeaders || !transcript.Handshake.RequireClose {
		t.Fatalf("signaling transcript %s must require exact handshake headers and close", fixture)
	}

	return transcript
}

func bindDiagnosticCLIRTCSteps(
	t *testing.T,
	fixture string,
	steps []replay.WSStep,
	peerSession *cliRTCPeerSession,
) {
	t.Helper()

	offerCount := 0
	closeCount := 0

	for index := range steps {
		step := &steps[index]
		if step.Frame != "text" || !step.Template {
			t.Fatalf("signaling transcript %s step %d must match a templated text frame", fixture, index)
		}

		var envelope struct {
			Method string `json:"method"`
		}

		err := json.Unmarshal(step.Body, &envelope)
		if err != nil {
			t.Fatalf("decode signaling transcript %s step %d envelope: %v", fixture, index, err)
		}

		switch envelope.Method {
		case liveViewMethod:
			if index != 0 || step.Kind != "expect" {
				t.Fatalf("signaling transcript %s has an unexpected live_view step", fixture)
			}

			offerCount++

			step.OnMatch = peerSession.negotiate
		case "close":
			if index != len(steps)-1 || step.Kind != "expect" {
				t.Fatalf("signaling transcript %s close is not the final client step", fixture)
			}

			closeCount++

			step.OnMatch = func(map[string]string) error { return peerSession.close() }
		}
	}

	if offerCount != 1 || closeCount != 1 {
		t.Fatalf(
			"signaling transcript %s must contain one offer and one final close; got %d and %d",
			fixture,
			offerCount,
			closeCount,
		)
	}
}

func (session *cliRTCPeerSession) negotiate(bindings map[string]string) error {
	offer, exists := bindings["offer"]
	if !exists || offer == "" {
		return cliRTCReplayError{reason: "offer SDP binding is empty", cause: nil}
	}

	peer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return cliRTCReplayError{reason: "create synthetic RTC peer", cause: err}
	}

	owned := false

	defer func() {
		if !owned {
			_ = peer.Close()
		}
	}()

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8},
		"video",
		"camera",
	)
	if err != nil {
		return cliRTCReplayError{reason: "create synthetic video track", cause: err}
	}

	_, err = peer.AddTrack(track)
	if err != nil {
		return cliRTCReplayError{reason: "add synthetic video track", cause: err}
	}

	err = peer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer})
	if err != nil {
		return cliRTCReplayError{reason: "apply client SDP offer", cause: err}
	}

	answer, err := peer.CreateAnswer(nil)
	if err != nil {
		return cliRTCReplayError{reason: "create synthetic SDP answer", cause: err}
	}

	gathered := webrtc.GatheringCompletePromise(peer)

	err = peer.SetLocalDescription(answer)
	if err != nil {
		return cliRTCReplayError{reason: "set synthetic SDP answer", cause: err}
	}

	timer := time.NewTimer(cliRTCNegotiationTimeout)
	defer timer.Stop()

	select {
	case <-gathered:
	case <-timer.C:
		return cliRTCReplayError{reason: "synthetic RTC peer ICE gathering timed out", cause: nil}
	}

	local := peer.LocalDescription()
	if local == nil || local.SDP == "" {
		return cliRTCReplayError{reason: "synthetic RTC peer produced no SDP answer", cause: nil}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	session.mu.Lock()

	if session.peer != nil {
		session.mu.Unlock()
		cancel()

		return cliRTCReplayError{reason: "synthetic RTC peer already exists", cause: nil}
	}

	session.peer, session.cancel, session.mediaDone = peer, cancel, done

	session.mu.Unlock()

	owned = true

	go session.writeSyntheticVideo(ctx, track, done)

	bindings["answer"] = local.SDP

	return nil
}

func (session *cliRTCPeerSession) writeSyntheticVideo(
	ctx context.Context,
	track *webrtc.TrackLocalStaticSample,
	done chan<- struct{},
) {
	defer close(done)

	ticker := time.NewTicker(cliRTCMediaInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = track.WriteSample(media.Sample{
				Data: []byte{0x10, 0x00, 0x00}, Duration: cliRTCMediaInterval,
				Timestamp: time.Time{}, PacketTimestamp: 0, PrevDroppedPackets: 0, Metadata: nil,
			})
		}
	}
}

func (session *cliRTCPeerSession) close() error {
	session.closeOnce.Do(func() {
		session.mu.Lock()
		peer, cancel, mediaDone := session.peer, session.cancel, session.mediaDone
		session.mu.Unlock()

		if cancel != nil {
			cancel()
		}

		if mediaDone != nil {
			timer := time.NewTimer(cliRTCMediaCloseTimeout)
			select {
			case <-mediaDone:
			case <-timer.C:
				session.closeErr = cliRTCReplayError{reason: "synthetic RTC media writer did not stop", cause: nil}
			}

			timer.Stop()
		}

		if peer != nil {
			err := peer.Close()
			if err != nil && session.closeErr == nil {
				session.closeErr = cliRTCReplayError{reason: "close synthetic RTC peer", cause: err}
			}
		}

		session.mu.Lock()
		session.closed = peer != nil && peer.ConnectionState() == webrtc.PeerConnectionStateClosed

		session.mu.Unlock()
	})

	return session.closeErr
}

func (session *cliRTCPeerSession) assertClosed() error {
	session.mu.Lock()
	peer, mediaDone, closed := session.peer, session.mediaDone, session.closed
	session.mu.Unlock()

	if peer == nil {
		return cliRTCReplayError{reason: "synthetic RTC peer was never negotiated", cause: nil}
	}

	if mediaDone == nil {
		return cliRTCReplayError{reason: "synthetic RTC media writer was never started", cause: nil}
	}

	select {
	case <-mediaDone:
	default:
		return cliRTCReplayError{reason: "synthetic RTC media writer remains active", cause: nil}
	}

	if !closed || peer.ConnectionState() != webrtc.PeerConnectionStateClosed {
		return cliRTCReplayError{reason: "synthetic RTC peer did not reach closed state", cause: nil}
	}

	if session.closeErr != nil {
		return session.closeErr
	}

	return nil
}
