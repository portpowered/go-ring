package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
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
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, privateFileMode)
	if err != nil {
		return nil, fmt.Errorf("create RTP recording: %w", err)
	}
	if err := file.Chmod(privateFileMode); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := restrictTokenFile(path); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	writer := bufio.NewWriter(file)
	if _, err := writer.WriteString(rtpRecordingMagic); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &rtpRecording{file: file, writer: writer}, nil
}

func (r *rtpRecording) WritePacket(packet *rtp.Packet) error {
	data, err := packet.Marshal()
	if err != nil {
		return err
	}
	if len(data) > maxRecordedPacket {
		return errors.New("RTP packet exceeds recording limit")
	}
	if err := binary.Write(r.writer, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	_, err = r.writer.Write(data)
	if err != nil {
		return err
	}
	return r.writer.Flush()
}

func (r *rtpRecording) Close() error {
	flushErr := r.writer.Flush()
	closeErr := r.file.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

func replayRTP(reader io.Reader, consume func(*rtp.Packet) error) error {
	magic := make([]byte, len(rtpRecordingMagic))
	if _, err := io.ReadFull(reader, magic); err != nil {
		return err
	}
	if string(magic) != rtpRecordingMagic {
		return errors.New("invalid RTP recording header")
	}
	for {
		var size uint32
		if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if size == 0 || size > maxRecordedPacket {
			return errors.New("invalid recorded RTP packet length")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return err
		}
		var packet rtp.Packet
		if err := packet.Unmarshal(data); err != nil {
			return err
		}
		if err := consume(&packet); err != nil {
			return err
		}
	}
}
