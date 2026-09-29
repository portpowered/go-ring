package main

import (
	"bytes"
	"testing"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v3"
)

func TestPreviewWritersDepacketizeWithoutUDP(t *testing.T) {
	t.Parallel()

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
			packet: newTestRTPPacket(0, 90000, true, 0x10, 0x00, 0x00, 0x00),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer

			writer, format, err := videoWriter(tt.codec, &output)
			if err != nil {
				t.Fatal(err)
			}

			if format != tt.format {
				t.Fatalf("format %q, want %q", format, tt.format)
			}

			err = writer.WriteRTP(tt.packet)
			if err != nil {
				t.Fatal(err)
			}

			err = writer.Close()
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.HasPrefix(output.Bytes(), tt.prefix) {
				t.Fatalf("output does not begin with %q", tt.prefix)
			}
		})
	}
}
