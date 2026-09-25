package ring

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/signaling"
)

const publicTestOffer = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\nc=IN IP4 0.0.0.0\r\na=mid:0\r\na=recvonly\r\n"

func newPublicTestSession(t *testing.T) (*DeviceSession, *signaling.Session) {
	t.Helper()
	core, err := signaling.NewSession(context.Background(), signaling.SessionConfig{DeviceID: 7, DialogID: "dialog", SignalID: "signal", ControlID: "control", Heartbeat: time.Minute, Send: func(context.Context, signaling.Message) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	conn := &SignalingConnection{done: make(chan struct{}), sessions: make(map[string]*DeviceSession)}
	s := &DeviceSession{connection: conn, core: core, dialogID: "dialog", deviceID: 7, signalID: "signal", offerSDP: publicTestOffer, iceMode: ICETrickle, movement: map[PTZAxis]string{}, done: make(chan struct{})}
	conn.sessions[s.dialogID] = s
	go s.watch()
	return s, core
}

func TestDeviceSessionReceiveAndCancel(t *testing.T) {
	s, core := newPublicTestSession(t)
	defer core.Close()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Receive(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Receive canceled error = %v", err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err := s.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait timeout = %v", err)
	}
	stop()
	body := json.RawMessage(`{"doorbot_id":7,"session_id":"signal","position":3}`)
	if err := core.Handle(signaling.Message{Method: "position_event", DialogID: "dialog", Body: body}); err != nil {
		t.Fatal(err)
	}
	event, err := s.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if event.Method != "position_event" || string(event.Body) != string(body) {
		t.Fatalf("received event = %+v", event)
	}
}

func TestDeviceSessionInputValidation(t *testing.T) {
	s, core := newPublicTestSession(t)
	defer core.Close()
	checks := []struct {
		name string
		run  func() error
	}{
		{"pan direction", func() error { _, err := s.PanStep(context.Background(), PanStepRequest{Direction: "UP"}); return err }},
		{"tilt direction", func() error {
			_, err := s.TiltStep(context.Background(), TiltStepRequest{Direction: "LEFT"})
			return err
		}},
		{"pan continuous direction", func() error {
			_, err := s.PanContinuous(context.Background(), PanContinuousRequest{Direction: "UP", Speed: 1})
			return err
		}},
		{"pan negative speed", func() error {
			_, err := s.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanLeft, Speed: -1})
			return err
		}},
		{"pan NaN speed", func() error {
			_, err := s.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanLeft, Speed: math.NaN()})
			return err
		}},
		{"pan infinite speed", func() error {
			_, err := s.PanContinuous(context.Background(), PanContinuousRequest{Direction: PanLeft, Speed: math.Inf(1)})
			return err
		}},
		{"tilt continuous direction", func() error {
			_, err := s.TiltContinuous(context.Background(), TiltContinuousRequest{Direction: "RIGHT", Speed: 1})
			return err
		}},
		{"tilt negative speed", func() error {
			_, err := s.TiltContinuous(context.Background(), TiltContinuousRequest{Direction: TiltUp, Speed: -1})
			return err
		}},
		{"tilt NaN speed", func() error {
			_, err := s.TiltContinuous(context.Background(), TiltContinuousRequest{Direction: TiltUp, Speed: math.NaN()})
			return err
		}},
		{"tilt infinite speed", func() error {
			_, err := s.TiltContinuous(context.Background(), TiltContinuousRequest{Direction: TiltUp, Speed: math.Inf(-1)})
			return err
		}},
		{"missing pan movement", func() error { _, err := s.StopPTZ(context.Background(), StopPTZRequest{Axis: PanAxis}); return err }},
		{"unknown axis", func() error { _, err := s.StopPTZ(context.Background(), StopPTZRequest{Axis: "zoom"}); return err }},
		{"empty options", func() error { return s.SetStreamOptions(context.Background(), SetStreamOptionsRequest{}) }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil {
				t.Fatal("invalid input was accepted")
			}
		})
	}
	if err := (&DeviceSession{iceMode: ICENonTrickle}).SendICE(context.Background(), ICECandidateRequest{Candidate: "candidate", MID: "0", MLineIndex: 0}); err == nil {
		t.Fatal("non-trickle ICE was accepted")
	}
	for _, candidate := range []ICECandidateRequest{{Candidate: "candidate", MID: "missing", MLineIndex: 0}, {Candidate: "candidate", MID: "0", MLineIndex: 1}, {Candidate: "", MID: "0", MLineIndex: 0}} {
		if err := s.SendICE(context.Background(), candidate); err == nil {
			t.Fatalf("invalid trickle candidate was accepted: %+v", candidate)
		}
	}
	badOffer := &DeviceSession{iceMode: ICETrickle, offerSDP: "not SDP"}
	if err := badOffer.SendICE(context.Background(), ICECandidateRequest{Candidate: "candidate", MID: "0", MLineIndex: 0}); err == nil {
		t.Fatal("invalid stored offer was accepted")
	}
}

func TestStartDeviceSessionValidationStopsBeforeTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connection := &SignalingConnection{}
	valid := StartDeviceSessionRequest{DeviceID: "7", Offer: SessionDescription{Type: "offer", SDP: publicTestOffer}}
	checks := []struct {
		name string
		ctx  context.Context
		req  StartDeviceSessionRequest
	}{
		{"canceled context", ctx, valid},
		{"invalid device", context.Background(), StartDeviceSessionRequest{DeviceID: "7x", Offer: valid.Offer}},
		{"wrong description type", context.Background(), StartDeviceSessionRequest{DeviceID: "7", Offer: SessionDescription{Type: "answer", SDP: publicTestOffer}}},
		{"empty SDP", context.Background(), StartDeviceSessionRequest{DeviceID: "7", Offer: SessionDescription{Type: "offer"}}},
		{"invalid ICE mode", context.Background(), StartDeviceSessionRequest{DeviceID: "7", Offer: valid.Offer, ICEMode: "gathering"}},
		{"invalid SDP", context.Background(), StartDeviceSessionRequest{DeviceID: "7", Offer: SessionDescription{Type: "offer", SDP: "broken"}}},
		{"negative max age", context.Background(), StartDeviceSessionRequest{DeviceID: "7", Offer: valid.Offer, MaxAge: -time.Second}},
		{"overlong max age", context.Background(), StartDeviceSessionRequest{DeviceID: "7", Offer: valid.Offer, MaxAge: signaling.MaxSessionAge + time.Second}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if _, err := connection.StartDeviceSession(check.ctx, check.req); err == nil {
				t.Fatal("invalid request reached signaling transport")
			}
		})
	}
}
func TestDeviceSessionRemoteCloseTerminatesMatchingSession(t *testing.T) {
	s, core := newPublicTestSession(t)
	defer core.Close()
	body := json.RawMessage(`{"doorbot_id":7,"session_id":"signal"}`)
	s.handle(signaling.Message{Method: "close", DialogID: "dialog", Body: body})
	if got := s.State(); got != SessionClosed {
		t.Fatalf("state after matching close = %q", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Wait(ctx); !errors.Is(err, signaling.ErrClosed) {
		t.Fatalf("Wait after close = %v", err)
	}

	other, otherCore := newPublicTestSession(t)
	defer otherCore.Close()
	other.handle(signaling.Message{Method: "close", DialogID: "dialog", Body: json.RawMessage(`{"doorbot_id":8,"session_id":"signal"}`)})
	if got := other.State(); got != SessionActive {
		t.Fatalf("unmatched close changed state to %q", got)
	}
}
