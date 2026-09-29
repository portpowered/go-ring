// Example rtc_stream generates a local Pion SDP offer and keeps Ring's
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
	"github.com/portpowered/go-ring/examples/internal/exampleerrors"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const localCandidateQueueCapacity = 128
const offerTimeout = 15 * time.Second

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	audio := flag.Bool("audio", false, "negotiate an audio transceiver as well as receive-only video")
	trickle := flag.Bool("trickle", false, "queue local ICE candidates during startup and send them after activation")
	flag.Parse()

	token, device := os.Getenv("RING_ACCESS_TOKEN"), os.Getenv("RING_DEVICE_ID")
	if token == "" || device == "" {
		return ringapimodels.NewBadRequestError("set RING_ACCESS_TOKEN and RING_DEVICE_ID", nil)
	}

	config, err := iceConfigurationFromEnvironment()
	if err != nil {
		return err
	}

	signalCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	ctx, fail := context.WithCancelCause(signalCtx)
	defer fail(nil)

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return exampleerrors.Wrap("create local WebRTC peer", err)
	}

	defer func() { _ = pc.Close() }()

	localCandidates := make(chan webrtc.ICECandidateInit, localCandidateQueueCapacity)

	if *trickle {
		pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
			if candidate == nil {
				return
			} // No unverified end-of-candidates wire message.

			select {
			case localCandidates <- candidate.ToJSON():
			case <-ctx.Done():
			default:
				fail(ringapimodels.NewConnectionError("local ICE candidate queue full", nil))
			}
		})
	}
	// Consuming packets keeps this example independent of a renderer. Applications
	// attach their own renderer/decoder and microphone track to the peer.
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			{
				_, _, err := track.ReadRTP()
				if err != nil {
					return
				}
			}
		}
	})

	offerCtx, offerCancel := context.WithTimeout(ctx, offerTimeout)
	offer, err := makeOffer(offerCtx, pc, *audio, *trickle)

	offerCancel()

	if err != nil {
		return err
	}

	client, err := ring.NewClient()
	if err != nil {
		return exampleerrors.Wrap("create Ring client", err)
	}

	auth := ring.AuthContext{AccessToken: token, HardwareID: ""}

	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
	if err != nil {
		return exampleerrors.Wrap("open Ring signaling", err)
	}

	defer func() { _ = conn.Close() }()

	mode := ring.ICENonTrickle
	if *trickle {
		mode = ring.ICETrickle
	}

	session, err := conn.StartDeviceSession(
		ctx,
		ring.StartDeviceSessionRequest{
			DeviceID:     device,
			Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer},
			AudioEnabled: *audio,
			VideoEnabled: true,
			MaxAge:       0,
			ICEMode:      mode,
		},
	)
	if err != nil {
		return exampleerrors.Wrap("start Ring device session", err)
	}

	defer func() { _ = session.Close() }()

	{
		err = pc.SetRemoteDescription(webrtc.SessionDescription{
			Type: webrtc.SDPTypeAnswer,
			SDP:  session.Answer().SDP,
		})
		if err != nil {
			return exampleerrors.Wrap("set remote WebRTC description", err)
		}
	}

	if *trickle {
		senderDone := forwardLocalCandidates(ctx, fail, localCandidates, session)

		defer func() { fail(nil); <-senderDone }()
	}

	fmt.Println("Signaling session active; Ctrl+C closes the session and local peer.")

	return receiveRemoteICE(ctx, session, pc)
}

func iceConfigurationFromEnvironment() (webrtc.Configuration, error) {
	config := webrtc.Configuration{}

	raw := os.Getenv("RING_ICE_SERVERS_JSON")

	if raw == "" {
		return config, nil
	}

	err := json.Unmarshal([]byte(raw), &config.ICEServers)
	if err != nil {
		return webrtc.Configuration{}, ringapimodels.NewBadRequestError("invalid RING_ICE_SERVERS_JSON", err)
	}

	return config, nil
}

func forwardLocalCandidates(
	ctx context.Context,
	fail context.CancelCauseFunc,
	candidates <-chan webrtc.ICECandidateInit,
	session *ring.DeviceSession,
) <-chan struct{} {
	senderDone := make(chan struct{})

	go func() {
		defer close(senderDone)

		for {
			select {
			case <-ctx.Done():
				return
			case candidate := <-candidates:
				if candidate.SDPMid == nil || candidate.SDPMLineIndex == nil {
					fail(ringapimodels.NewBadRequestError("local candidate lacks MID or index", nil))

					return
				}

				err := session.SendICE(
					ctx,
					ring.ICECandidateRequest{
						Candidate:  candidate.Candidate,
						MID:        *candidate.SDPMid,
						MLineIndex: int(*candidate.SDPMLineIndex),
					},
				)
				if err != nil {
					fail(err)

					return
				}
			}
		}
	}()

	return senderDone
}

func receiveRemoteICE(ctx context.Context, session *ring.DeviceSession, pc *webrtc.PeerConnection) error {
	for {
		event, err := session.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				cause := context.Cause(ctx)
				if !errors.Is(cause, context.Canceled) {
					return exampleerrors.Wrap("Ring signaling session ended", cause)
				}

				return nil
			}

			if errors.Is(err, ring.ErrSessionClosed) {
				return nil
			}

			return exampleerrors.Wrap("receive remote ICE event", err)
		}

		if event.Method != "ice" {
			continue
		}

		var body struct {
			Candidate string `json:"ice"`
			Index     uint16 `json:"mlineindex"`
		}

		{
			err := json.Unmarshal(event.Body, &body)
			if err != nil {
				return ringapimodels.NewConnectionError("invalid remote ICE event", err)
			}
		}

		if body.Candidate == "" {
			return ringapimodels.NewConnectionError("invalid remote ICE event", nil)
		}

		{
			err = pc.AddICECandidate(webrtc.ICECandidateInit{
				Candidate:     body.Candidate,
				SDPMLineIndex: &body.Index,
			})
			if err != nil {
				return exampleerrors.Wrap("add remote ICE candidate", err)
			}
		}
	}
}

func makeOffer(ctx context.Context, pc *webrtc.PeerConnection, audio, trickle bool) (string, error) {
	if audio {
		{
			_, err := pc.AddTransceiverFromKind(
				webrtc.RTPCodecTypeAudio,
				webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv},
			)
			if err != nil {
				return "", exampleerrors.Wrap("add audio transceiver", err)
			}
		}
	}

	{
		_, err := pc.AddTransceiverFromKind(
			webrtc.RTPCodecTypeVideo,
			webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
		)
		if err != nil {
			return "", exampleerrors.Wrap("add receive-only video transceiver", err)
		}
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return "", exampleerrors.Wrap("create WebRTC offer", err)
	}

	var gathered <-chan struct{}
	if !trickle {
		gathered = webrtc.GatheringCompletePromise(pc)
	}

	{
		err = pc.SetLocalDescription(offer)
		if err != nil {
			return "", exampleerrors.Wrap("set local WebRTC description", err)
		}
	}

	if trickle {
		return pc.LocalDescription().SDP, nil
	}

	select {
	case <-gathered:
	case <-ctx.Done():
		return "", exampleerrors.Wrap("wait for ICE gathering", ctx.Err())
	}

	return pc.LocalDescription().SDP, nil
}
