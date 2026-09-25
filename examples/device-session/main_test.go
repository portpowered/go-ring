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
		offer, err := makeOffer(ctx, pc, audio, false)
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

func TestTrickleProfileBuffersPeerCandidatesWithMIDAndIndex(t *testing.T) {
	var settings webrtc.SettingEngine
	settings.SetIncludeLoopbackCandidate(true)
	var media webrtc.MediaEngine
	if err := media.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(settings), webrtc.WithMediaEngine(&media)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	candidates := make(chan webrtc.ICECandidateInit, 128)
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			candidates <- candidate.ToJSON()
		}
	})
	gathered := webrtc.GatheringCompletePromise(pc)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	offer, err := makeOffer(ctx, pc, false, true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := signaling.ParseSDP(offer)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if len(candidates) == 0 {
		t.Fatal("no buffered local candidates")
	}
	for len(candidates) > 0 {
		candidate := <-candidates
		if candidate.SDPMid == nil || candidate.SDPMLineIndex == nil {
			t.Fatal("missing candidate identity")
		}
		if err = signaling.ValidateICE(parsed, *candidate.SDPMid, int(*candidate.SDPMLineIndex)); err != nil {
			t.Fatal(err)
		}
	}
}
