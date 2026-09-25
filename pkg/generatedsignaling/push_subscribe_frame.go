package generatedsignaling

type PushSubscribeFrame struct {
	Method               string                 `json:"method" binding:"required"`
	DialogId             string                 `json:"dialog_id" binding:"required"`
	Riid                 string                 `json:"riid,omitempty"`
	Body                 *PushSubscribeBody     `json:"body" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
