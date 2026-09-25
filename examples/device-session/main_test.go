package main

import (
	"context"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/portpowered/go-ring/internal/signaling"
)

func TestOfferProfilesUseLocalPeerDescriptions(t *testing.T) {
	for _, audio := range []bool{false, true} {
		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		offer, err := makeOffer(ctx, pc, audio)
		cancel()
		if err != nil {
			pc.Close()
			t.Fatal(err)
		}
		if _, err = signaling.ParseSDP(offer); err != nil {
			pc.Close()
			t.Fatal(err)
		}
		transceivers := pc.GetTransceivers()
		expected := 1
		if audio {
			expected = 2
		}
		if len(transceivers) != expected {
			t.Fatalf("profile has %d transceivers", len(transceivers))
		}
		video := transceivers[len(transceivers)-1]
		if video.Kind() != webrtc.RTPCodecTypeVideo || video.Direction() != webrtc.RTPTransceiverDirectionRecvonly {
			t.Fatal("invalid video profile")
		}
		if audio && (transceivers[0].Kind() != webrtc.RTPCodecTypeAudio || transceivers[0].Direction() != webrtc.RTPTransceiverDirectionSendrecv) {
			t.Fatal("invalid audio profile")
		}
		if pc.LocalDescription().SDP != offer {
			t.Fatal("offer is not gathered local description")
		}
		pc.Close()
	}
}
