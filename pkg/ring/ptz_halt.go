package ring

import (
	"encoding/json"

	"github.com/portpowered/go-ring/internal/protocol"
	generatedsignaling "github.com/portpowered/go-ring/pkg/dependencymodels/signaling"
)

// PTZHaltLimitReached is the halt reason Ring sends when a turn reaches the end of the camera's range.
const PTZHaltLimitReached = "LIMIT_REACHED"

// PTZDirection names the end of an axis a PTZHalt reports: PanLeft or PanRight for the pan axis,
// TiltUp or TiltDown for the tilt axis.
type PTZDirection string

// PTZHalt is Ring's notice that an axis sits at an end of its travel (PTZ.Pan.Halted or
// PTZ.Tilt.Halted, Reason PTZHaltLimitReached). Measured on a pan-tilt camera on 2026-11-13, the
// camera does not send it when the axis arrives: it sends one for each axis at an end when a later
// PTZ command reaches it, such as the stop that ends the turn or a turn on the other axis. A caller
// can take it as a known position at that moment, but not as the time the axis got there.
type PTZHalt struct {
	Axis      PTZAxis
	Direction PTZDirection
	Reason    string
	// SessionID is the control session whose turn halted.
	SessionID string
}

// PTZHalt reads the event as Ring's PTZ.Pan.Halted or PTZ.Tilt.Halted notice. Ring sends the live
// notice wrapped as command.message (seen on a camera 2026-11-13); recorded sessions also hold it
// unwrapped. It reports false for any other event.
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

	command, ok := haltCommand(body[protocol.FieldCommand])
	if !ok {
		return none, false
	}

	direction, _ := command.Params[protocol.FieldDirection].(string)
	reason, _ := command.Params[protocol.FieldReason].(string)
	sessionID, _ := command.Params[protocol.FieldSessionIDRPC].(string)

	axis, ok := haltAxis(command.Method, direction)
	if !ok {
		return none, false
	}

	return PTZHalt{Axis: axis, Direction: PTZDirection(direction), Reason: reason, SessionID: sessionID}, true
}

// haltAxis reports the axis of a halt notice, if the method is one and the direction names an end
// of that axis.
func haltAxis(method, direction string) (PTZAxis, bool) {
	switch method {
	case protocol.RPCPanHalted:
		return PanAxis, direction == protocol.PanLeft || direction == protocol.PanRight
	case protocol.RPCTiltHalted:
		return TiltAxis, direction == protocol.TiltUp || direction == protocol.TiltDown
	default:
		return "", false
	}
}

// haltCommand decodes an rpc command, unwrapping command.message when Ring wraps it.
func haltCommand(raw json.RawMessage) (generatedsignaling.ServerRpcCommand, bool) {
	var command generatedsignaling.ServerRpcCommand

	err := json.Unmarshal(raw, &command)
	if err != nil {
		return command, false
	}

	if command.Method != "" {
		return command, true
	}

	var wrapper struct {
		Message *generatedsignaling.ServerRpcCommand `json:"message"`
	}

	err = json.Unmarshal(raw, &wrapper)
	if err != nil || wrapper.Message == nil {
		return command, false
	}

	return *wrapper.Message, true
}
