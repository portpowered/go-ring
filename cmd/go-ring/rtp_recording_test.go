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
	data, err := os.ReadFile(path) // #nosec G304 -- path is created inside this test's temporary directory.
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
	for _, packetData := range []struct {
		sequence  uint16
		timestamp uint32
		payload   []byte
	}{
		{sequence: 1, timestamp: 1, payload: []byte{0x67, 0x42, 0, 0x1f, 0x80}},
		{sequence: 2, timestamp: 2, payload: []byte{0x68, 0xc0}},
		{sequence: 3, timestamp: 3, payload: []byte{0x65, 0xb8}},
	} {
		packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: packetData.sequence, Timestamp: packetData.timestamp, Marker: true}, Payload: packetData.payload}
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
	data, err := os.ReadFile(outputPath) // #nosec G304 -- outputPath is created inside this test's temporary directory.
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte{0, 0, 0, 1, 0x65, 0xb8}) {
		t.Fatal("replay did not produce the IDR slice")
	}
}
