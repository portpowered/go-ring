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
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer pc.Close()
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
	offer, err := makeOffer(offerCtx, pc, *audio)
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
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{DeviceID: device, Offer: ring.SessionDescription{Type: "offer", SDP: offer}, AudioEnabled: *audio, VideoEnabled: true, ICEMode: ring.ICENonTrickle})
	if err != nil {
		return err
	}
	defer session.Close()
	if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: session.Answer().SDP}); err != nil {
		return err
	}
	fmt.Println("Signaling session active; Ctrl+C closes the session and local peer.")
	for {
		event, err := session.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ring.ErrSessionClosed) {
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

func makeOffer(ctx context.Context, pc *webrtc.PeerConnection, audio bool) (string, error) {
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
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		return "", err
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return pc.LocalDescription().SDP, nil
}
