package generatedsignaling

type PushEventPayload struct {
	DeviceId             string                 `json:"device_id,omitempty"`
	LocationId           string                 `json:"location_id,omitempty"`
	StartTime            string                 `json:"start_time,omitempty"`
	EndTime              string                 `json:"end_time,omitempty"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
