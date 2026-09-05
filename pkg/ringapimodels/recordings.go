package ringapimodels

import (
	"io"
	"net/http"
)

// Recording represents a recording/event from a Ring device
type Recording struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"` // "motion", "ding", "on_demand"
	Answered  bool   `json:"answered"`
	CreatedAt string `json:"created_at"`
	DeviceID  int64  `json:"device_id"`
}

// VideoStream represents a video stream response from the Ring API
type VideoStream struct {
	Body        io.ReadCloser
	ContentType string
	ContentLen  int64
	Headers     http.Header
}

// RecordingHistoryResponse represents the response from listing recordings
type RecordingHistoryResponse struct {
	Recordings []*Recording `json:"recordings"`
}
