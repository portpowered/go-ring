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
	"github.com/portpowered/go-ring/examples/internal/exampleerrors"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

const iceGatheringTimeout = 15 * time.Second
const stopPTZTimeout = 3 * time.Second

func main() {
	err := run()
	if err != nil {
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
		err := json.Unmarshal([]byte(raw), &config.ICEServers)
		if err != nil {
			return ringapimodels.NewBadRequestError("invalid RING_ICE_SERVERS_JSON", err)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return exampleerrors.Wrap("create local WebRTC peer", err)
	}

	defer func() { _ = pc.Close() }()

	{
		_, err = pc.AddTransceiverFromKind(
			webrtc.RTPCodecTypeVideo,
			webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
		)
		if err != nil {
			return exampleerrors.Wrap("add receive-only video transceiver", err)
		}
	}

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

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return exampleerrors.Wrap("create WebRTC offer", err)
	}

	gathered := webrtc.GatheringCompletePromise(pc)

	{
		err = pc.SetLocalDescription(offer)
		if err != nil {
			return exampleerrors.Wrap("set local WebRTC description", err)
		}
	}

	select {
	case <-gathered:
	case <-ctx.Done():
		return exampleerrors.Wrap("wait for ICE gathering", ctx.Err())
	case <-time.After(iceGatheringTimeout):
		return ringapimodels.NewConnectionError("timed out gathering local ICE candidates", context.DeadlineExceeded)
	}

	return runDeviceSession(ctx, pc, token, deviceID)
}

func runDeviceSession(ctx context.Context, pc *webrtc.PeerConnection, token, deviceID string) error {
	client, err := ring.NewClient()
	if err != nil {
		return exampleerrors.Wrap("create Ring client", err)
	}

	auth := ring.AuthContext{AccessToken: token, HardwareID: ""}

	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
	if err != nil {
		return exampleerrors.Wrap("open Ring signaling", err)
	}

	defer closeIgnoringError(conn.Close)

	session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
		DeviceID: deviceID,
		Offer: ring.SessionDescription{
			Type: ring.SDPTypeOffer,
			SDP:  pc.LocalDescription().SDP,
		},
		AudioEnabled: false,
		VideoEnabled: true,
		MaxAge:       0,
		ICEMode:      ring.ICENonTrickle,
	})
	if err != nil {
		return exampleerrors.Wrap("start Ring device session", err)
	}

	defer closeIgnoringError(session.Close)

	answer := session.Answer()
	if answer.Type != ring.SDPTypeAnswer {
		return ringapimodels.NewBadRequestError(fmt.Sprintf("unexpected SDP type: %s", answer.Type), nil)
	}

	err = pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer.SDP})
	if err != nil {
		return exampleerrors.Wrap("set remote WebRTC description", err)
	}

	{
		_, err = session.PanContinuous(ctx, ring.PanContinuousRequest{Direction: ring.PanRight, Speed: 0.5})
		if err != nil {
			return exampleerrors.Wrap("pan camera right", err)
		}
	}

	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
	}
	// Stop even if Ctrl+C canceled the session context.
	err = stopPan(ctx, session)
	if err != nil {
		return err
	}

	return nil
}

func stopPan(ctx context.Context, session *ring.DeviceSession) error {
	stopCtx, stopCancel := context.WithTimeout(context.WithoutCancel(ctx), stopPTZTimeout)
	defer stopCancel()

	_, err := session.StopPTZ(stopCtx, ring.StopPTZRequest{Axis: ring.PanAxis})
	if err != nil {
		return exampleerrors.Wrap("stop camera pan", err)
	}

	return nil
}

func closeIgnoringError(closeFunc func() error) {
	_ = closeFunc()
}
