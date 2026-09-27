package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pion/rtp"
)

func TestRTPRecordingReplaysExactPackets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture.rtp")
	recording, err := newRTPRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: 42, Timestamp: 99, Marker: true}, Payload: []byte{0x65, 0xb8}}
	if err := recording.WritePacket(packet); err != nil {
		t.Fatal(err)
	}
	// A CLI exit may happen before Close, so each packet must already be readable.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var received []*rtp.Packet
	if err := replayRTP(bytes.NewReader(data), func(p *rtp.Packet) error {
		received = append(received, p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(received) != 1 || received[0].SequenceNumber != packet.SequenceNumber || !bytes.Equal(received[0].Payload, packet.Payload) {
		t.Fatalf("replayed packets = %+v", received)
	}
	if err := recording.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplayVideoCommandProducesH264(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "capture.rtp")
	recording, err := newRTPRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	for sequence, payload := range [][]byte{
		{0x67, 0x42, 0, 0x1f, 0x80},
		{0x68, 0xc0},
		{0x65, 0xb8},
	} {
		packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(sequence + 1), Timestamp: uint32(sequence + 1), Marker: true}, Payload: payload}
		if err := recording.WritePacket(packet); err != nil {
			t.Fatal(err)
		}
	}
	if err := recording.Close(); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(directory, "video.h264")
	if err := replayVideoCommand([]string{path, "--output", outputPath}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte{0, 0, 0, 1, 0x65, 0xb8}) {
		t.Fatal("replay did not produce the IDR slice")
	}
}
