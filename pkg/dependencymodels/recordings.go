package dependencymodels

// RingDoorbot represents the doorbot/device information in a recording
type RingDoorbot struct {
	ID          int64  `json:"id"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

// RingRecording represents a recording in the Ring API response
type RingRecording struct {
	ID        int64       `json:"id"`
	Kind      string      `json:"kind"`
	Answered  bool        `json:"answered"`
	CreatedAt string      `json:"created_at"`
	DeviceID  int64       `json:"-"` // Populated from doorbot.id during conversion
	Doorbot   RingDoorbot `json:"doorbot"`
}

// RingRecordingHistoryResponse represents the raw response from the Ring API
// The API returns an array directly, not wrapped in an object
type RingRecordingHistoryResponse struct {
	Recordings []RingRecording `json:"-"`
}

// RingRecordingURLResponse represents the response containing a signed recording URL
type RingRecordingURLResponse struct {
	URL string `json:"url"`
}
