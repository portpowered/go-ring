package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media/h264writer"
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
		return h264writer.NewWith(output), previewH264, nil
	case previewIVF:
		writer, err := ivfwriter.NewWith(output, ivfwriter.WithCodec(webrtc.MimeTypeVP8))
		return writer, previewIVF, err
	default:
		return nil, "", fmt.Errorf("unsupported preview format %s", format)
	}
}

func previewFormat(codec string) (string, error) {
	switch strings.ToUpper(codec) {
	case strings.ToUpper(webrtc.MimeTypeH264):
		return previewH264, nil
	case strings.ToUpper(webrtc.MimeTypeVP8):
		return previewIVF, nil
	default:
		return "", fmt.Errorf("ffplay preview does not support negotiated codec %s", codec)
	}
}

func playTrack(ctx context.Context, track *webrtc.TrackRemote) error {
	if _, err := exec.LookPath(ffplayCommand); err != nil {
		return errors.New("ffplay is required for preview; install FFmpeg or use --player none")
	}
	codec := track.Codec().MimeType
	format, err := previewFormat(codec)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, ffplayCommand, "-loglevel", "error", "-f", format, "-i", "pipe:0")
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
		if err := writer.WriteRTP(packet); err != nil {
			return err
		}
	}
}
