package ring

import (
	"encoding/json"
	"fmt"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// String returns the wire name of a known SDP type.
func (t SDPType) String() string {
	switch t {
	case SDPTypeOffer:
		return protocol.SDPTypeOffer
	case SDPTypeAnswer:
		return protocol.SDPTypeAnswer
	default:
		return ""
	}
}

func (t SDPType) MarshalJSON() ([]byte, error) {
	if t.String() == "" {
		return nil, ringapimodels.NewBadRequestError(fmt.Sprintf("invalid SDP type %d", t), nil)
	}
	return json.Marshal(t.String())
}

func (t *SDPType) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return ringapimodels.NewBadRequestError("invalid SDP type encoding", err)
	}
	switch value {
	case protocol.SDPTypeOffer:
		*t = SDPTypeOffer
	case protocol.SDPTypeAnswer:
		*t = SDPTypeAnswer
	default:
		return ringapimodels.NewBadRequestError(fmt.Sprintf("invalid SDP type %q", value), nil)
	}
	return nil
}
