package main

import (
	"bytes"
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

func TestPreviewWritersDepacketizeWithoutUDP(t *testing.T) {
	tests := []struct {
		name   string
		codec  string
		format string
		prefix []byte
		packet *rtp.Packet
	}{
		{
			name: "VP8", codec: webrtc.MimeTypeVP8, format: "ivf",
			prefix: []byte("DKIF"),
			packet: &rtp.Packet{Header: rtp.Header{Version: 2, Marker: true, Timestamp: 90000}, Payload: []byte{0x10, 0x00, 0x00, 0x00}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			writer, format, err := videoWriter(tt.codec, &output)
			if err != nil {
				t.Fatal(err)
			}
			if format != tt.format {
				t.Fatalf("format %q, want %q", format, tt.format)
			}
			if err := writer.WriteRTP(tt.packet); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(output.Bytes(), tt.prefix) {
				t.Fatalf("output does not begin with %q", tt.prefix)
			}
		})
	}
}
