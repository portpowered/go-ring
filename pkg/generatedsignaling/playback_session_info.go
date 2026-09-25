package generatedsignaling

type PlaybackSessionInfo struct {
	PingInterval         int                    `json:"ping_interval,omitempty"`
	Region               string                 `json:"region,omitempty"`
	AdditionalProperties map[string]interface{} `json:"-,omitempty"`
}
