package webrtc_test

import (
	"strings"
	"testing"

	"github.com/pion/sdp/v3"
	"github.com/portpowered/go-ring/pkg/dependencies/webrtc"
)

const (
	sessionHeader           = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE a b\r\n"
	sdpSendReceiveDirection = "sendrecv"
	sdpSendOnlyDirection    = "sendonly"
	sdpRecvOnlyDirection    = "recvonly"
	sdpInactiveDirection    = "inactive"
)

func audio(mid, dir string) string {
	return "m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:" + mid +
		"\r\na=" + dir +
		"\r\na=rtpmap:111 opus/48000/2\r\na=x-fixture:keep\r\n"
}

func mediaDirection(description *sdp.SessionDescription, media *sdp.MediaDescription) string {
	for _, attributes := range [][]sdp.Attribute{media.Attributes, description.Attributes} {
		for _, attribute := range attributes {
			switch attribute.Key {
			case sdpSendReceiveDirection, sdpSendOnlyDirection, sdpRecvOnlyDirection, sdpInactiveDirection:
				return attribute.Key
			}
		}
	}

	return sdpSendReceiveDirection
}

func TestNormalizeAnswerMatchesMIDNotKind(t *testing.T) {
	t.Parallel()

	offer := sessionHeader + audio("a", sdpRecvOnlyDirection) + audio("b", sdpSendReceiveDirection)
	answer := sessionHeader + audio("a", sdpSendReceiveDirection) + audio("b", sdpSendReceiveDirection)

	got, err := webrtc.NormalizeAnswer(offer, answer)
	if err != nil {
		t.Fatal(err)
	}

	description, err := webrtc.ParseSDP(got)
	if err != nil {
		t.Fatal(err)
	}

	if mediaDirection(description, description.MediaDescriptions[0]) != sdpSendOnlyDirection ||
		mediaDirection(description, description.MediaDescriptions[1]) != sdpSendReceiveDirection {
		t.Fatal("corrected wrong media section")
	}

	if strings.Count(got, "a=x-fixture:keep") != 2 {
		t.Fatal("lost extension")
	}

	for _, test := range []struct{ name, value string }{
		{"duplicate-mid", strings.Replace(offer, "a=mid:b", "a=mid:a", 1)},
		{"unknown-bundle", strings.Replace(offer, "BUNDLE a b", "BUNDLE a c", 1)},
		{"conflicting-direction", strings.Replace(offer, "a=recvonly", "a=recvonly\r\na=sendrecv", 1)},
		{"no-media", "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n"},
		{"invalid", "secret-value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			{
				_, err := webrtc.ParseSDP(test.value)
				if err == nil {
					t.Fatal("accepted invalid SDP")
				}
			}
		})
	}
}

func TestAnswerOrderRejectionAndICE(t *testing.T) {
	t.Parallel()

	offer := sessionHeader + audio("a", sdpRecvOnlyDirection) + audio("b", sdpSendReceiveDirection)
	{
		_, err := webrtc.NormalizeAnswer(
			offer,
			sessionHeader+audio("b", sdpSendReceiveDirection)+audio("a", sdpSendReceiveDirection),
		)
		if err == nil {
			t.Fatal("accepted reordered media")
		}
	}

	answer := sessionHeader + audio("a", sdpSendOnlyDirection) + audio("b", sdpSendReceiveDirection)

	got, err := webrtc.NormalizeAnswer(offer, answer)

	if err != nil || got != answer {
		t.Fatal("changed valid answer", err)
	}

	direction, _ := webrtc.ParseSDP(offer)
	for _, tc := range []struct {
		mid   string
		index int
		valid bool
	}{{"a", 0, true}, {"", 1, true}, {"b", 0, false}, {"a", -1, false}, {"a", 2, false}} {
		err := webrtc.ValidateICE(direction, tc.mid, tc.index)
		if (err == nil) != tc.valid {
			t.Errorf("mid=%q index=%d err=%v", tc.mid, tc.index, err)
		}
	}
}

func TestRejectedAndSessionDirection(t *testing.T) {
	t.Parallel()

	offer := sessionHeader + audio("a", sdpRecvOnlyDirection) + audio("b", sdpRecvOnlyDirection)
	answer := sessionHeader + "a=sendrecv\r\n" + strings.Replace(
		audio("a", "sendrecv"),
		"a=sendrecv\r\n",
		"",
		1,
	) + strings.Replace(
		audio("b", "sendrecv"),
		"m=audio 9",
		"m=audio 0",
		1,
	)

	got, err := webrtc.NormalizeAnswer(offer, answer)
	if err != nil {
		t.Fatal(err)
	}

	d, _ := webrtc.ParseSDP(got)
	if mediaDirection(d, d.MediaDescriptions[0]) != sdpSendOnlyDirection ||
		mediaDirection(d, d.MediaDescriptions[1]) != sdpSendReceiveDirection {
		t.Fatal("wrong inherited/rejected direction")
	}
}

func FuzzParseSDP(f *testing.F) {
	f.Add(sessionHeader + audio("a", sdpRecvOnlyDirection) + audio("b", sdpSendReceiveDirection))
	f.Add("not-sdp")
	f.Fuzz(func(t *testing.T, raw string) { _, _ = webrtc.ParseSDP(raw) })
}

func TestAnswerDirectionMatrix(t *testing.T) {
	t.Parallel()

	header := strings.Replace(sessionHeader, "BUNDLE a b", "BUNDLE a", 1)

	directions := []string{
		sdpSendReceiveDirection,
		sdpSendOnlyDirection,
		sdpRecvOnlyDirection,
		sdpInactiveDirection,
	}
	for _, offerDirection := range directions {
		for _, answerDirection := range directions {
			t.Run(offerDirection+"/"+answerDirection, func(t *testing.T) {
				t.Parallel()

				allowed := offerDirection == sdpSendReceiveDirection || answerDirection == sdpInactiveDirection ||
					offerDirection == sdpSendOnlyDirection && answerDirection == sdpRecvOnlyDirection ||
					offerDirection == sdpRecvOnlyDirection &&
						(answerDirection == sdpSendOnlyDirection || answerDirection == sdpSendReceiveDirection)

				_, err := webrtc.NormalizeAnswer(
					header+audio("a", offerDirection),
					header+audio("a", answerDirection),
				)

				if (err == nil) != allowed {
					t.Fatalf("allowed=%v error=%v", allowed, err)
				}
			})
		}
	}

	rejected := strings.Replace(header+audio("a", sdpSendReceiveDirection), "m=audio 9", "m=audio 0", 1)
	{
		_, err := webrtc.NormalizeAnswer(rejected, header+audio("a", sdpSendReceiveDirection))
		if err == nil {
			t.Fatal("answer reactivated a rejected stream")
		}
	}

	{
		_, err := webrtc.NormalizeAnswer(rejected, rejected)
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		_, err := webrtc.ParseSDP(header + "a=sendonly\r\na=recvonly\r\n" + audio("a", sdpSendReceiveDirection))
		if err == nil {
			t.Fatal("accepted conflicting session directions")
		}
	}
}

func TestSDPRejectsMultipleMIDAttributesInOneSection(t *testing.T) {
	t.Parallel()

	raw := strings.Replace(sessionHeader, "BUNDLE a b", "BUNDLE a", 1) + audio("a", sdpRecvOnlyDirection) + "a=mid:b\r\n"
	{
		_, err := webrtc.ParseSDP(raw)
		if err == nil {
			t.Fatal("accepted ambiguous MID identity")
		}
	}
}
