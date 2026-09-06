package dependencymodels

// RingDevicesResponse represents the raw response from the Ring API
type RingDevicesResponse struct {
	Doorbots           []RingDevice `json:"doorbots"`
	AuthorizedDoorbots []RingDevice `json:"authorized_doorbots"`
	Chimes             []RingDevice `json:"chimes"`
	StickupCams        []RingDevice `json:"stickup_cams"`
	Other              []RingDevice `json:"other"`
}

// RingDevice represents a device in the Ring API response
type RingDevice struct {
	ID                     int64                  `json:"id"`
	Name                   string                 `json:"name"`
	Description            string                 `json:"description"` // Used when name is not present (e.g., for other devices)
	Kind                   string                 `json:"kind"`        // Device kind (e.g., "intercom_handset_audio")
	Family                 string                 `json:"family"`
	Owned                  *bool                  `json:"owned,omitempty"`
	Address                string                 `json:"address"`
	Timezone               string                 `json:"timezone"`
	TimeZone               string                 `json:"time_zone"` // Alternative field name used by some devices
	WifiName               string                 `json:"wifi_name"`
	WifiSignalStrength     int                    `json:"wifi_signal_strength"`
	Volume                 int                    `json:"volume"`
	HasLight               bool                   `json:"has_light"`
	LightBrightness        *int                   `json:"light_brightness,omitempty"`
	MotionDetectionEnabled bool                   `json:"motion_detection_enabled"`
	Health                 map[string]interface{} `json:"health,omitempty"`
}
