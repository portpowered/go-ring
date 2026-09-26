package ring

import (
	"encoding/json"
	"testing"
)

func TestSessionDescriptionSDPTypeJSON(t *testing.T) {
	for _, test := range []struct {
		typ  SDPType
		wire string
	}{
		{SDPTypeOffer, `{"type":"offer","sdp":"test"}`},
		{SDPTypeAnswer, `{"type":"answer","sdp":"test"}`},
	} {
		encoded, err := json.Marshal(SessionDescription{Type: test.typ, SDP: "test"})
		if err != nil || string(encoded) != test.wire {
			t.Fatalf("marshal %v: %s, %v", test.typ, encoded, err)
		}
		var decoded SessionDescription
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Type != test.typ {
			t.Fatalf("unmarshal %s: %+v, %v", encoded, decoded, err)
		}
	}
	if _, err := json.Marshal(SessionDescription{Type: SDPTypeUnknown}); err == nil {
		t.Fatal("unknown SDP type was serialized")
	}
	var description SessionDescription
	if err := json.Unmarshal([]byte(`{"type":"bogus","sdp":"test"}`), &description); err == nil {
		t.Fatal("unknown SDP type was accepted")
	}
}
