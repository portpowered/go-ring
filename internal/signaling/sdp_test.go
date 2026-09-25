package signaling

import (
	"strings"
	"testing"
)

const sessionHeader = "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\na=group:BUNDLE a b\r\n"

func audio(mid, dir string) string {
	return "m=audio 9 UDP/TLS/RTP/SAVPF 111\r\na=mid:" + mid + "\r\na=" + dir + "\r\na=rtpmap:111 opus/48000/2\r\na=x-fixture:keep\r\n"
}

func TestNormalizeAnswerMatchesMIDNotKind(t *testing.T) {
	offer := sessionHeader + audio("a", "recvonly") + audio("b", "sendrecv")
	answer := sessionHeader + audio("a", "sendrecv") + audio("b", "sendrecv")
	got, err := NormalizeAnswer(offer, answer)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseSDP(got)
	if err != nil {
		t.Fatal(err)
	}
	if direction(d, d.MediaDescriptions[0]) != "sendonly" || direction(d, d.MediaDescriptions[1]) != "sendrecv" {
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
			if _, err := ParseSDP(test.value); err == nil {
				t.Fatal("accepted invalid SDP")
			}
		})
	}
}

func TestAnswerOrderRejectionAndICE(t *testing.T) {
	offer := sessionHeader + audio("a", "recvonly") + audio("b", "sendrecv")
	if _, err := NormalizeAnswer(offer, sessionHeader+audio("b", "sendrecv")+audio("a", "sendrecv")); err == nil {
		t.Fatal("accepted reordered media")
	}
	answer := sessionHeader + audio("a", "sendonly") + audio("b", "sendrecv")
	got, err := NormalizeAnswer(offer, answer)
	if err != nil || got != answer {
		t.Fatal("changed valid answer", err)
	}
	d, _ := ParseSDP(offer)
	for _, tc := range []struct {
		mid   string
		index int
		valid bool
	}{{"a", 0, true}, {"", 1, true}, {"b", 0, false}, {"a", -1, false}, {"a", 2, false}} {
		if err := ValidateICE(d, tc.mid, tc.index); (err == nil) != tc.valid {
			t.Errorf("mid=%q index=%d err=%v", tc.mid, tc.index, err)
		}
	}
}

func TestRejectedAndSessionDirection(t *testing.T) {
	offer := sessionHeader + audio("a", "recvonly") + audio("b", "recvonly")
	answer := sessionHeader + "a=sendrecv\r\n" + strings.Replace(audio("a", "sendrecv"), "a=sendrecv\r\n", "", 1) + strings.Replace(audio("b", "sendrecv"), "m=audio 9", "m=audio 0", 1)
	got, err := NormalizeAnswer(offer, answer)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ParseSDP(got)
	if direction(d, d.MediaDescriptions[0]) != "sendonly" || direction(d, d.MediaDescriptions[1]) != "sendrecv" {
		t.Fatal("wrong inherited/rejected direction")
	}
}

func FuzzParseSDP(f *testing.F) {
	f.Add(sessionHeader + audio("a", "recvonly") + audio("b", "sendrecv"))
	f.Add("not-sdp")
	f.Fuzz(func(t *testing.T, raw string) { _, _ = ParseSDP(raw) })
}

func TestAnswerDirectionMatrix(t *testing.T) {
	header := strings.Replace(sessionHeader, "BUNDLE a b", "BUNDLE a", 1)
	for _, offerDirection := range []string{"sendrecv", "sendonly", "recvonly", "inactive"} {
		for _, answerDirection := range []string{"sendrecv", "sendonly", "recvonly", "inactive"} {
			t.Run(offerDirection+"/"+answerDirection, func(t *testing.T) {
				allowed := offerDirection == "sendrecv" || answerDirection == "inactive" || offerDirection == "sendonly" && answerDirection == "recvonly" || offerDirection == "recvonly" && (answerDirection == "sendonly" || answerDirection == "sendrecv")
				_, err := NormalizeAnswer(header+audio("a", offerDirection), header+audio("a", answerDirection))
				if (err == nil) != allowed {
					t.Fatalf("allowed=%v error=%v", allowed, err)
				}
			})
		}
	}
	rejected := strings.Replace(header+audio("a", "sendrecv"), "m=audio 9", "m=audio 0", 1)
	if _, err := NormalizeAnswer(rejected, header+audio("a", "sendrecv")); err == nil {
		t.Fatal("answer reactivated a rejected stream")
	}
	if _, err := NormalizeAnswer(rejected, rejected); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSDP(header + "a=sendonly\r\na=recvonly\r\n" + audio("a", "sendrecv")); err == nil {
		t.Fatal("accepted conflicting session directions")
	}
}
