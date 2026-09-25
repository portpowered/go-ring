package generatedsignaling

type LiveViewBody struct {
	DoorbotId            int                    `json:"doorbot_id" binding:"required"`
	Sdp                  string                 `json:"sdp" binding:"required"`
	StreamOptions        *LiveStreamOptions     `json:"stream_options" binding:"required"`
	ReservedType         string                 `json:"type" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
