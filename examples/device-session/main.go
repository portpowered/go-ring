// Example device-session generates a local Pion SDP offer and keeps Ring's
// signaling session alive. It does not render video or capture microphone audio.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/portpowered/go-ring/pkg/ring"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	audio := flag.Bool("audio", false, "negotiate an audio transceiver as well as receive-only video")
	trickle := flag.Bool("trickle", false, "queue local ICE candidates during startup and send them after activation")
	ptzDemo := flag.Bool("ptz-demo", false, "pan right continuously for one second, then stop")
	flag.Parse()
	token, device := os.Getenv("RING_ACCESS_TOKEN"), os.Getenv("RING_DEVICE_ID")
	if token == "" || device == "" {
		return errors.New("set RING_ACCESS_TOKEN and RING_DEVICE_ID")
	}
	config := webrtc.Configuration{}
	if raw := os.Getenv("RING_ICE_SERVERS_JSON"); raw != "" {
		if json.Unmarshal([]byte(raw), &config.ICEServers) != nil {
			return errors.New("invalid RING_ICE_SERVERS_JSON")
		}
	}
	signalCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	ctx, fail := context.WithCancelCause(signalCtx)
	defer fail(nil)
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer pc.Close()
	localCandidates := make(chan webrtc.ICECandidateInit, 128)
	if *trickle {
		pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
			if candidate == nil {
				return
			} // No unverified end-of-candidates wire message.
			select {
			case localCandidates <- candidate.ToJSON():
			case <-ctx.Done():
			default:
				fail(errors.New("local ICE candidate queue full"))
			}
		})
	}
	// Consuming packets keeps this example independent of a renderer. Applications
	// attach their own renderer/decoder and microphone track to the peer.
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
		}
	})
	offerCtx, offerCancel := context.WithTimeout(ctx, 15*time.Second)
	offer, err := makeOffer(offerCtx, pc, *audio, *trickle)
	offerCancel()
	if err != nil {
		return err
	}
	client, err := ring.NewClient(ring.WithAccessToken(token))
	if err != nil {
		return err
	}
	defer client.Close()
	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{})
	if err != nil {
		return err
	}
	defer conn.Close()
	mode := ring.ICENonTrickle
	if *trickle {
		mode = ring.ICETrickle
	}
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: device, Offer: ring.SessionDescription{Type: "offer", SDP: offer}, AudioEnabled: *audio, VideoEnabled: true, ICEMode: mode})
	if err != nil {
		return err
	}
	defer session.Close()
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: session.Answer().SDP}); err != nil {
		return err
	}
	if *ptzDemo {
		// Continuous movement persists until explicitly stopped. Always send a
		// stop command, including when the wait is interrupted.
		if _, err := session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: 0.5}); err != nil {
			return err
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer stopCancel()
		if _, err := session.StopPTZ(stopCtx, ring.StopPTZRequest{Axis: ring.PanAxis}); err != nil {
			return err
		}
	}
	if *trickle {
		senderDone := make(chan struct{})
		go func() {
			defer close(senderDone)
			for {
				select {
				case <-ctx.Done():
					return
				case candidate := <-localCandidates:
					if candidate.SDPMid == nil || candidate.SDPMLineIndex == nil {
						fail(errors.New("local candidate lacks MID or index"))
						return
					}
					err := session.SendICE(ctx, ring.ICECandidateRequest{Candidate: candidate.Candidate, MID: *candidate.SDPMid, MLineIndex: int(*candidate.SDPMLineIndex)})
					if err != nil {
						fail(err)
						return
					}
				}
			}
		}()
		defer func() { fail(nil); <-senderDone }()
	}
	fmt.Println("Signaling session active; Ctrl+C closes the session and local peer.")
	for {
		event, err := session.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				if cause := context.Cause(ctx); !errors.Is(cause, context.Canceled) {
					return cause
				}
				return nil
			}
			if errors.Is(err, ring.ErrSessionClosed) {
				return nil
			}
			return err
		}
		if event.Method != "ice" {
			continue
		}
		var body struct {
			Candidate string `json:"ice"`
			Index     uint16 `json:"mlineindex"`
		}
		if json.Unmarshal(event.Body, &body) != nil || body.Candidate == "" {
			return errors.New("invalid remote ICE event")
		}
		if err = pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: body.Candidate, SDPMLineIndex: &body.Index}); err != nil {
			return err
		}
	}
}

func makeOffer(ctx context.Context, pc *webrtc.PeerConnection, audio, trickle bool) (string, error) {
	if audio {
		if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv}); err != nil {
			return "", err
		}
	}
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return "", err
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	var gathered <-chan struct{}
	if !trickle {
		gathered = webrtc.GatheringCompletePromise(pc)
	}
	if err = pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	if trickle {
		return pc.LocalDescription().SDP, nil
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return pc.LocalDescription().SDP, nil
}
