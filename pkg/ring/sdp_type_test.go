package ring_test

import (
	"encoding/json"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

func TestSessionDescriptionSDPTypeJSON(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		typ  ring.SDPType
		wire string
	}{
		{ring.SDPTypeOffer, `{"type":"offer","sdp":"test"}`},
		{ring.SDPTypeAnswer, `{"type":"answer","sdp":"test"}`},
	} {
		encoded, err := json.Marshal(ring.SessionDescription{Type: test.typ, SDP: "test"})
		if err != nil || string(encoded) != test.wire {
			t.Fatalf("marshal %v: %s, %v", test.typ, encoded, err)
		}

		var decoded ring.SessionDescription
		{
			err := json.Unmarshal(encoded, &decoded)
			if err != nil || decoded.Type != test.typ {
				t.Fatalf("unmarshal %s: %+v, %v", encoded, decoded, err)
			}
		}
	}

	{
		_, err := json.Marshal(ring.SessionDescription{Type: ring.SDPTypeUnknown})
		if err == nil {
			t.Fatal("unknown SDP type was serialized")
		}
	}

	var description ring.SessionDescription

	err := json.Unmarshal([]byte(`{"type":"bogus","sdp":"test"}`), &description)
	if err == nil {
		t.Fatal("unknown SDP type was accepted")
	}
}
