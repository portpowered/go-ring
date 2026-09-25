package generatedsignaling

type PushFilter struct {
	FilterIdentifier     string                 `json:"filter_identifier" binding:"required"`
	Filters              *PushFilters           `json:"filters" binding:"required"`
	NotificationScope    string                 `json:"notification_scope" binding:"required"`
	NotificationType     string                 `json:"notification_type" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
