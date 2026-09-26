package generatedsignaling

import (
	"encoding/json"
)

type PtzDirection uint

const (
	PtzDirectionLeft PtzDirection = iota
	PtzDirectionRight
	PtzDirectionUp
	PtzDirectionDown
)

// Value returns the value of the enum.
func (op PtzDirection) Value() any {
	if op >= PtzDirection(len(PtzDirectionValues)) {
		return nil
	}
	return PtzDirectionValues[op]
}

var PtzDirectionValues = []any{"LEFT", "RIGHT", "UP", "DOWN"}
var ValuesToPtzDirection = map[any]PtzDirection{
	PtzDirectionValues[PtzDirectionLeft]:  PtzDirectionLeft,
	PtzDirectionValues[PtzDirectionRight]: PtzDirectionRight,
	PtzDirectionValues[PtzDirectionUp]:    PtzDirectionUp,
	PtzDirectionValues[PtzDirectionDown]:  PtzDirectionDown,
}

func (op *PtzDirection) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToPtzDirection[v]
	return nil
}

func (op PtzDirection) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
