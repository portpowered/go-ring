package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"github.com/pion/rtp"
)

const (
	rtpRecordingMagic = "go-ring-rtp-v1\n"
	maxRecordedPacket = 64 << 10
)

// rtpRecording captures the exact RTP packets delivered to the preview writer.
// The file contains private camera video and is created with owner-only access.
type rtpRecording struct {
	file   *os.File
	writer *bufio.Writer
}

func newRTPRecording(path string) (*rtpRecording, error) {
	file, err := os.OpenFile(
		path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode,
	) // #nosec G304 -- User-selected private recording path.
	if err != nil {
		return nil, wrapCommandError("create RTP recording", err)
	}

	err = file.Chmod(privateFileMode)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)

		return nil, wrapCommandError("set RTP recording permissions", err)
	}

	err = restrictTokenFile(path)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)

		return nil, wrapCommandError("set RTP recording permissions", err)
	}

	writer := bufio.NewWriter(file)

	_, err = writer.WriteString(rtpRecordingMagic)
	if err != nil {
		_ = file.Close()

		return nil, wrapCommandError("write RTP recording header", err)
	}

	return &rtpRecording{file: file, writer: writer}, nil
}

func (r *rtpRecording) WritePacket(packet *rtp.Packet) error {
	data, err := packet.Marshal()
	if err != nil {
		return wrapCommandError("marshal RTP packet", err)
	}

	if len(data) > maxRecordedPacket {
		return commandError("RTP packet exceeds recording limit")
	}
	// #nosec G115 -- Packet length is checked against the 64 KiB recording limit above.
	err = binary.Write(r.writer, binary.BigEndian, uint32(len(data)))
	if err != nil {
		return wrapCommandError("write RTP packet length", err)
	}

	_, err = r.writer.Write(data)
	if err != nil {
		return wrapCommandError("write RTP packet", err)
	}

	err = r.writer.Flush()
	if err != nil {
		return wrapCommandError("flush RTP recording", err)
	}

	return nil
}

func (r *rtpRecording) Close() error {
	flushErr := r.writer.Flush()
	closeErr := r.file.Close()

	if flushErr != nil {
		return wrapCommandError("flush RTP recording", flushErr)
	}

	if closeErr != nil {
		return wrapCommandError("close RTP recording", closeErr)
	}

	return nil
}

func replayRTP(reader io.Reader, consume func(*rtp.Packet) error) error {
	magic := make([]byte, len(rtpRecordingMagic))

	_, err := io.ReadFull(reader, magic)
	if err != nil {
		return wrapCommandError("read RTP recording header", err)
	}

	if string(magic) != rtpRecordingMagic {
		return commandError("invalid RTP recording header")
	}

	for {
		var size uint32

		err = binary.Read(reader, binary.BigEndian, &size)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return wrapCommandError("read RTP packet length", err)
		}

		if size == 0 || size > maxRecordedPacket {
			return commandError("invalid recorded RTP packet length")
		}

		data := make([]byte, size)

		_, err = io.ReadFull(reader, data)
		if err != nil {
			return wrapCommandError("read RTP packet", err)
		}

		var packet rtp.Packet

		err = packet.Unmarshal(data)
		if err != nil {
			return wrapCommandError("decode RTP packet", err)
		}

		err = consume(&packet)
		if err != nil {
			return wrapCommandError("process RTP packet", err)
		}
	}
}
