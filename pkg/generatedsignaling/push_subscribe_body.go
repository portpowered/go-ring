package generatedsignaling

type PushSubscribeBody struct {
	RequestedNotifications []PushFilter           `json:"requested_notifications" binding:"required"`
	AdditionalProperties   map[string]interface{} `json:"-,omitempty"`
}
