package generatedsignaling

type SessionNotificationBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	SessionId            string                 `json:"session_id" binding:"required"`
	IsOk                 bool                   `json:"is_ok" binding:"required"`
	Text                 string                 `json:"text" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
