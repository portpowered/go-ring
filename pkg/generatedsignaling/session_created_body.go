package generatedsignaling

type SessionCreatedBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	SessionId            string                 `json:"session_id" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
