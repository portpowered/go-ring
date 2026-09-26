package generatedsignaling

type LiveAnswerInfo struct {
	SessionId            string                 `json:"session_id" binding:"required"`
	PingInterval         int                    `json:"ping_interval,omitempty"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
