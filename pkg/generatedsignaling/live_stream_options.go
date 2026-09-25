package generatedsignaling

type LiveStreamOptions struct {
	AudioEnabled         bool                   `json:"audio_enabled,omitempty"`
	VideoEnabled         bool                   `json:"video_enabled,omitempty"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
