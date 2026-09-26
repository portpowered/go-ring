package generatedsignaling

type PlaybackCloseReason struct {
	Code                 int                    `json:"code" binding:"required"`
	Text                 string                 `json:"text" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
