package generatedsignaling

type LiveIceFrame struct {
	Method               string                 `json:"method" binding:"required"`
	DialogId             string                 `json:"dialog_id" binding:"required"`
	Riid                 string                 `json:"riid,omitempty"`
	Body                 *LiveIceBody           `json:"body" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
