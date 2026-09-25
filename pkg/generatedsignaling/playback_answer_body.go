package generatedsignaling

type PlaybackAnswerBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	SessionId            string                 `json:"session_id" binding:"required"`
	ReservedType         string                 `json:"type" binding:"required"`
	Sdp                  string                 `json:"sdp" binding:"required"`
	SessionInfo          *PlaybackSessionInfo   `json:"session_info,omitempty"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
