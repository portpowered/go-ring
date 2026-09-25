package generatedsignaling

type LiveViewFrame struct {
	Method               string                 `json:"method" binding:"required"`
	DialogId             string                 `json:"dialog_id" binding:"required"`
	Body                 *LiveViewBody          `json:"body" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
