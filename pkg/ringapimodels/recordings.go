package ringapimodels

import (
	"io"
	"net/http"
)

// VideoStream represents a video stream response from the Ring API
type VideoStream struct {
	Body        io.ReadCloser
	ContentType string
	ContentLen  int64
	Headers     http.Header
}
