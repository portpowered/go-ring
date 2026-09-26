package replay_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/signaling"
)

func recordedAnswerSDP(t *testing.T) (string, string) {
	t.Helper()
	offer, captured := recordedLiveView(t)
	var frame struct {
		Body struct {
			SDP string `json:"sdp"`
		} `json:"body"`
	}
	if err := json.Unmarshal(captured["sdp"], &frame); err != nil {
		t.Fatal(err)
	}
	return offer, frame.Body.SDP
}

func TestRecordedSDPAnswerDirectionVariants(t *testing.T) {
	offer, answer := recordedAnswerSDP(t)
	video := strings.Index(answer, "m=video")
	if video < 0 {
		t.Fatal("recorded answer has no video section")
	}
	for _, tc := range []struct {
		name, offer, answer string
		valid               bool
	}{
		{"captured answer", offer, answer, true},
		{"recvonly workaround", offer, answer[:video] + strings.Replace(answer[video:], "a=sendonly", "a=sendrecv", 1), true},
		{"inactive offered video", strings.Replace(offer, "a=recvonly", "a=inactive", 1), answer, false},
		{"sendonly offered video", strings.Replace(offer, "a=recvonly", "a=sendonly", 1), answer, false},
		{"recvonly answered video", offer, answer[:video] + strings.Replace(answer[video:], "a=sendonly", "a=recvonly", 1), false},
		{"rejected offered video reactivated", strings.Replace(offer, "m=video 33618", "m=video 0", 1), answer, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.offer == offer && tc.answer == answer && tc.name != "captured answer" {
				t.Fatal("mutation missed capture")
			}
			got, err := signaling.NormalizeAnswer(tc.offer, tc.answer)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("invalid SDP accepted: %q", got)
			}
			if tc.name == "recvonly workaround" && !strings.Contains(got, "a=sendonly") {
				t.Fatal("video direction was not normalized")
			}
		})
	}
}
