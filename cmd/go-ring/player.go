package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/ivfwriter"
)

type rtpVideoWriter interface {
	WriteRTP(*rtp.Packet) error
	Close() error
}

const (
	previewH264 = "h264"
	previewIVF  = "ivf"
)

func videoWriter(codec string, output io.Writer) (rtpVideoWriter, string, error) {
	format, err := previewFormat(codec)
	if err != nil {
		return nil, "", err
	}
	switch format {
	case previewH264:
		return newH264FrameWriter(output), previewH264, nil
	case previewIVF:
		writer, err := ivfwriter.NewWith(output, ivfwriter.WithCodec(webrtc.MimeTypeVP8))
		return writer, previewIVF, err
	default:
		return nil, "", commandError("unsupported preview format " + format)
	}
}

func previewFormat(codec string) (string, error) {
	switch strings.ToUpper(codec) {
	case strings.ToUpper(webrtc.MimeTypeH264):
		return previewH264, nil
	case strings.ToUpper(webrtc.MimeTypeVP8):
		return previewIVF, nil
	default:
		return "", commandError("ffplay preview does not support negotiated codec " + codec)
	}
}

func playTrack(ctx context.Context, track *webrtc.TrackRemote, recordPath string) error {
	var recording *rtpRecording
	if recordPath != "" {
		var err error
		recording, err = newRTPRecording(recordPath)
		if err != nil {
			return err
		}
		defer func() { _ = recording.Close() }()
	}
	if _, err := exec.LookPath(ffplayCommand); err != nil {
		return commandError("ffplay is required for preview; install FFmpeg or use --player none")
	}
	codec := track.Codec().MimeType
	format, err := previewFormat(codec)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, ffplayCommand, "-loglevel", "error", "-f", format, "-i", "pipe:0") // #nosec G204 -- ffplayCommand is fixed and format is allowlisted by previewFormat.
	command.Stderr = os.Stderr
	input, err := command.StdinPipe()
	if err != nil {
		return err
	}
	writer, _, err := videoWriter(codec, input)
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		_ = input.Close()
		return err
	}
	defer func() {
		_ = writer.Close()
		_ = input.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()
	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			return err
		}
		if recording != nil {
			if err := recording.WritePacket(packet); err != nil {
				return err
			}
		}
		if err := writer.WriteRTP(packet); err != nil {
			return err
		}
	}
}
