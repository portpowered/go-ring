// Example rtc_ptz opens a live camera session, pans right briefly, and stops.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const iceGatheringTimeout = 15 * time.Second
const stopPTZTimeout = 3 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	token, deviceID := os.Getenv("RING_ACCESS_TOKEN"), os.Getenv("RING_DEVICE_ID")
	if token == "" || deviceID == "" {
		return ringapimodels.NewBadRequestError("set RING_ACCESS_TOKEN and RING_DEVICE_ID", nil)
	}

	config := webrtc.Configuration{}
	if raw := os.Getenv("RING_ICE_SERVERS_JSON"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &config.ICEServers); err != nil {
			return ringapimodels.NewBadRequestError("invalid RING_ICE_SERVERS_JSON", err)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return err
	}
	defer func() { _ = pc.Close() }()
	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return err
	}
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
		}
	})
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(offer); err != nil {
		return err
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(iceGatheringTimeout):
		return ringapimodels.NewConnectionError("timed out gathering local ICE candidates", context.DeadlineExceeded)
	}

	client, err := ring.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	auth := ring.AuthContext{AccessToken: token}
	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID: deviceID,
		Offer: ring.SessionDescription{
			Type: ring.SDPTypeOffer,
			SDP:  pc.LocalDescription().SDP,
		},
		VideoEnabled: true,
		ICEMode:      ring.ICENonTrickle,
	})
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	if answer := session.Answer(); answer.Type != ring.SDPTypeAnswer {
		return ringapimodels.NewBadRequestError(fmt.Sprintf("unexpected SDP type: %s", answer.Type), nil)
	} else if err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		return err
	}

	if _, err = session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: 0.5}); err != nil {
		return err
	}
	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
	}
	// Stop even if Ctrl+C canceled the session context.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), stopPTZTimeout)
	defer stopCancel()
	_, err = session.StopPTZ(stopCtx, ring.StopPTZRequest{Axis: ring.PanAxis})
	return err
}
