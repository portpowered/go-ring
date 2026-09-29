package replay_test

import (
	"encoding/json"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

func TestRecordedSDPTypeRoundTripAndInvalidVariants(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		method string
		typeID ring.SDPType
	}{
		{liveViewMethod, ring.SDPTypeOffer},
		{"sdp", ring.SDPTypeAnswer},
	} {
		var captured string
		if tc.method == liveViewMethod {
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

				err := json.Unmarshal(row.Payload.Body, &body)
				if err != nil {
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
		{
			err := json.Unmarshal(encoded, &decoded)
			if err != nil || decoded != tc.typeID {
				t.Fatalf("SDP type round trip = %v, %v", decoded, err)
			}
		}
	}

	{
		_, err := json.Marshal(ring.SDPType(99))
		if err == nil {
			t.Fatal("unknown SDP type was serialized")
		}
	}

	for _, raw := range []string{`"invalid"`, `42`} {
		var decoded ring.SDPType

		err := json.Unmarshal([]byte(raw), &decoded)
		if err == nil {
			t.Fatalf("invalid SDP type %s was accepted", raw)
		}
	}
}
