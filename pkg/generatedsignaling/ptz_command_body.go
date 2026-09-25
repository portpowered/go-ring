package generatedsignaling

type PtzCommandBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	SessionId            string                 `json:"session_id" binding:"required"`
	Command              *PtzWireCommand        `json:"command" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
