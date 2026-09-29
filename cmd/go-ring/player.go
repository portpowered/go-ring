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
	WriteRTP(packet *rtp.Packet) error
	Close() error
}

type videoWriterHandle struct {
	writer rtpVideoWriter
}

func (w videoWriterHandle) WriteRTP(packet *rtp.Packet) error {
	err := w.writer.WriteRTP(packet)
	if err != nil {
		return wrapCommandError("write preview video packet", err)
	}

	return nil
}

func (w videoWriterHandle) Close() error {
	err := w.writer.Close()
	if err != nil {
		return wrapCommandError("close preview writer", err)
	}

	return nil
}

const (
	previewH264 = "h264"
	previewIVF  = "ivf"
)

func videoWriter(codec string, output io.Writer) (*videoWriterHandle, string, error) {
	format, err := previewFormat(codec)
	if err != nil {
		return nil, "", err
	}

	switch format {
	case previewH264:
		return &videoWriterHandle{writer: newH264FrameWriter(output)}, previewH264, nil
	case previewIVF:
		writer, err := ivfwriter.NewWith(output, ivfwriter.WithCodec(webrtc.MimeTypeVP8))
		if err != nil {
			return nil, "", wrapCommandError("create IVF preview writer", err)
		}

		return &videoWriterHandle{writer: writer}, previewIVF, nil
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
			return wrapCommandError("start RTP recording", err)
		}

		defer func() { _ = recording.Close() }()
	}

	_, err := exec.LookPath(ffplayCommand)
	if err != nil {
		return wrapCommandError("ffplay is required for preview; install FFmpeg or use --player none", err)
	}

	codec := track.Codec().MimeType

	format, err := previewFormat(codec)
	if err != nil {
		return err
	}

	command := exec.CommandContext(
		ctx, ffplayCommand, "-loglevel", "error", "-f", format, "-i", "pipe:0",
	) // #nosec G204 -- fixed command and previewFormat allowlist.
	command.Stderr = os.Stderr

	input, err := command.StdinPipe()
	if err != nil {
		return wrapCommandError("open ffplay input", err)
	}

	writer, _, err := videoWriter(codec, input)
	if err != nil {
		return wrapCommandError("create preview writer", err)
	}

	err = command.Start()
	if err != nil {
		_ = input.Close()

		return wrapCommandError("start ffplay", err)
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
			return wrapCommandError("read video RTP packet", err)
		}

		if recording != nil {
			err = recording.WritePacket(packet)
			if err != nil {
				return wrapCommandError("record video RTP packet", err)
			}
		}

		err = writer.WriteRTP(packet)
		if err != nil {
			return err
		}
	}
}
