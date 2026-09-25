package generatedsignaling

type PushSubscriptionBody struct {
	SubscriptionId       string                 `json:"subscription_id" binding:"required"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
