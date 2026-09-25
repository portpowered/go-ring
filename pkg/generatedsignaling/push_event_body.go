package generatedsignaling

type PushEventBody struct {
	FilterIdentifiers    []string               `json:"filter_identifiers,omitempty"`
	IngestionTimeMs      int                    `json:"ingestion_time_ms,omitempty"`
	NotificationScope    string                 `json:"notification_scope" binding:"required"`
	NotificationType     string                 `json:"notification_type" binding:"required"`
	Payload              *PushEventPayload      `json:"payload" binding:"required"`
	SubscriptionId       string                 `json:"subscription_id" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
