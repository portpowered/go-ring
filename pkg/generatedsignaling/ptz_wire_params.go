package generatedsignaling

type PtzWireParams struct {
	SessionId            string                 `json:"sessionId" binding:"required"`
	Timestamp            float64                `json:"timestamp" binding:"required"`
	Version              float64                `json:"version" binding:"required"`
	Direction            string                 `json:"direction" binding:"required"`
	Speed                float64                `json:"speed,omitempty"`
	Reason               string                 `json:"reason,omitempty"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
