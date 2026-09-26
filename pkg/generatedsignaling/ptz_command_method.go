package generatedsignaling

import (
	"encoding/json"
)

type PtzCommandMethod uint

const (
	PtzCommandMethodPtzDotPanDotStep PtzCommandMethod = iota
	PtzCommandMethodPtzDotPanDotContinuous
	PtzCommandMethodPtzDotTiltDotStep
	PtzCommandMethodPtzDotTiltDotContinuous
)

// Value returns the value of the enum.
func (op PtzCommandMethod) Value() any {
	if op >= PtzCommandMethod(len(PtzCommandMethodValues)) {
		return nil
	}
	return PtzCommandMethodValues[op]
}

var PtzCommandMethodValues = []any{"PTZ.Pan.Step", "PTZ.Pan.Continuous", "PTZ.Tilt.Step", "PTZ.Tilt.Continuous"}
var ValuesToPtzCommandMethod = map[any]PtzCommandMethod{
	PtzCommandMethodValues[PtzCommandMethodPtzDotPanDotStep]:        PtzCommandMethodPtzDotPanDotStep,
	PtzCommandMethodValues[PtzCommandMethodPtzDotPanDotContinuous]:  PtzCommandMethodPtzDotPanDotContinuous,
	PtzCommandMethodValues[PtzCommandMethodPtzDotTiltDotStep]:       PtzCommandMethodPtzDotTiltDotStep,
	PtzCommandMethodValues[PtzCommandMethodPtzDotTiltDotContinuous]: PtzCommandMethodPtzDotTiltDotContinuous,
}

func (op *PtzCommandMethod) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToPtzCommandMethod[v]
	return nil
}

func (op PtzCommandMethod) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
