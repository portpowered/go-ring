package ring

import (
	"encoding/json"

	"github.com/portpowered/go-ring/internal/protocol"
	generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
)

// PTZHaltLimitReached is the halt reason Ring sends when a turn reaches the end of the camera's range.
const PTZHaltLimitReached = "LIMIT_REACHED"

// PTZHalt is Ring's notice that a continuous pan stopped on its own (PTZ.Pan.Halted). Ring sends it
// when the camera reaches the end of its range (Reason PTZHaltLimitReached), so a caller can mark
// that end as a known position. Other reasons may appear. Ring has not been seen to send a tilt halt.
type PTZHalt struct {
	Axis      PTZAxis
	Direction PanDirection
	Reason    string
	// SessionID is the control session whose turn halted.
	SessionID string
}

// PTZHalt reads the event as Ring's PTZ.Pan.Halted notice. It reports false for any other event.
func (e SessionEvent) PTZHalt() (PTZHalt, bool) {
	var none PTZHalt

	if e.Method != protocol.MethodRPC {
		return none, false
	}

	var body map[string]json.RawMessage

	err := json.Unmarshal(e.Body, &body)
	if err != nil {
		return none, false
	}

	var command generatedsignaling.ServerRpcCommand

	err = json.Unmarshal(body[protocol.FieldCommand], &command)
	if err != nil || command.Method != protocol.RPCPanHalted {
		return none, false
	}

	direction, _ := command.Params[protocol.FieldDirection].(string)
	reason, _ := command.Params[protocol.FieldReason].(string)
	sessionID, _ := command.Params[protocol.FieldSessionIDRPC].(string)

	if PanDirection(direction) != PanLeft && PanDirection(direction) != PanRight {
		return none, false
	}

	return PTZHalt{Axis: PanAxis, Direction: PanDirection(direction), Reason: reason, SessionID: sessionID}, true
}
