package generatedsignaling

type SessionNotificationFrame struct {
	Method               string                   `json:"method" binding:"required"`
	DialogId             string                   `json:"dialog_id" binding:"required"`
	Riid                 string                   `json:"riid,omitempty"`
	Body                 *SessionNotificationBody `json:"body" binding:"required"`
	AdditionalProperties map[string]interface{}   `json:"-,omitempty"`
}
