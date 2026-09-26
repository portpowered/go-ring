package replay_test

import (
	"encoding/json"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

func TestRecordedSDPTypeRoundTripAndInvalidVariants(t *testing.T) {
	for _, tc := range []struct {
		method string
		typeID ring.SDPType
	}{
		{"live_view", ring.SDPTypeOffer},
		{"sdp", ring.SDPTypeAnswer},
	} {
		var captured string
		if tc.method == "live_view" {
			// The capture omits type on live_view; the SDK sends "offer".
			captured = "offer"
		} else {
			for _, row := range loadConversation(t, "flow-402.json").Messages {
				if row.Payload.Method != tc.method {
					continue
				}
				var body struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(row.Payload.Body, &body); err != nil {
					t.Fatal(err)
				}
				if body.Type != "" {
					captured = body.Type
					break
				}
			}
		}
		if captured == "" || tc.typeID.String() != captured {
			t.Fatalf("captured %s SDP type = %q", tc.method, captured)
		}
		encoded, err := json.Marshal(tc.typeID)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ring.SDPType
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != tc.typeID {
			t.Fatalf("SDP type round trip = %v, %v", decoded, err)
		}
	}
	if _, err := json.Marshal(ring.SDPType(99)); err == nil {
		t.Fatal("unknown SDP type was serialized")
	}
	for _, raw := range []string{`"invalid"`, `42`} {
		var decoded ring.SDPType
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			t.Fatalf("invalid SDP type %s was accepted", raw)
		}
	}
}
