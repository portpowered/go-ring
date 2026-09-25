package generatedsignaling

type PushSubscriptionAckBody struct {
	Status               string                 `json:"status" binding:"required"`
	SubscriptionId       string                 `json:"subscription_id" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
