package main

import (
	"bytes"
	"context"
	"image/jpeg"
	"os/exec"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/portpowered/go-ring/pkg/ring"
)

type frameResult struct {
	image []byte
	err   error
}

func captureRTCSnapshot(
	parent context.Context,
	client *ring.Client,
	auth ring.AuthContext,
	deviceID string,
	iceFile string,
	timeout time.Duration,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	config, err := videoConfiguration(iceFile)
	if err != nil {
		return nil, err
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, wrapCommandError("create snapshot peer connection", err)
	}

	defer func() { _ = pc.Close() }()

	frames := make(chan frameResult, 1)

	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		image, err := captureTrackFrame(ctx, track)
		select {
		case frames <- frameResult{image: image, err: err}:
		default:
		}
	})

	_, err = pc.AddTransceiverFromKind(
		webrtc.RTPCodecTypeVideo,
		webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly},
	)
	if err != nil {
		return nil, wrapCommandError("add snapshot video transceiver", err)
	}

	conn, session, err := startVideoSession(ctx, client, auth, deviceID, pc)
	if err != nil {
		return nil, err
	}

	defer func() { _ = conn.Close() }()
	defer func() { _ = session.Close() }()

	events := make(chan error, 1)
	go receiveICE(ctx, session, pc, events)

	select {
	case result := <-frames:
		return result.image, result.err
	case err := <-events:
		return nil, err
	case <-ctx.Done():
		return nil, wrapCommandError("live snapshot timed out before a decodable frame", ctx.Err())
	}
}

func captureTrackFrame(ctx context.Context, track *webrtc.TrackRemote) ([]byte, error) {
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, wrapCommandError("ffmpeg is required for live snapshots", err)
	}

	format, err := previewFormat(track.Codec().MimeType)
	if err != nil {
		return nil, err
	}

	command := exec.CommandContext(
		ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", format,
		"-i", "pipe:0", "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	) // #nosec G204 -- format is restricted to previewFormat's codec allowlist.

	var image, diagnostics bytes.Buffer

	command.Stdout = &image
	command.Stderr = &diagnostics

	input, err := command.StdinPipe()
	if err != nil {
		return nil, wrapCommandError("open ffmpeg input", err)
	}

	writer, _, err := videoWriter(track.Codec().MimeType, input)
	if err != nil {
		_ = input.Close()

		return nil, wrapCommandError("create snapshot video writer", err)
	}

	err = command.Start()
	if err != nil {
		_ = input.Close()

		return nil, wrapCommandError("start ffmpeg", err)
	}

	go func() {
		defer func() { _ = writer.Close() }()

		for {
			packet, _, readErr := track.ReadRTP()
			if readErr != nil || writer.WriteRTP(packet) != nil {
				return
			}
		}
	}()

	err = command.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return nil, wrapCommandError("capture live frame", ctx.Err())
		}

		return nil, wrapCommandErrorDetail("ffmpeg could not decode the live frame", err, diagnostics.String())
	}

	_, err = jpeg.DecodeConfig(bytes.NewReader(image.Bytes()))
	if err != nil {
		return nil, wrapCommandError("live view did not produce a JPEG frame", err)
	}

	return image.Bytes(), nil
}
