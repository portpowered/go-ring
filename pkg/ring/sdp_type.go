package ring

import (
	"encoding/json"
	"fmt"

	"github.com/portpowered/go-ring/internal/protocol"
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
		return nil, fmt.Errorf("invalid SDP type %d", t)
	}
	return json.Marshal(t.String())
}

func (t *SDPType) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	switch value {
	case protocol.SDPTypeOffer:
		*t = SDPTypeOffer
	case protocol.SDPTypeAnswer:
		*t = SDPTypeAnswer
	default:
		return fmt.Errorf("invalid SDP type %q", value)
	}
	return nil
}
