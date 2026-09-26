package ringmedia

import (
	"io"
	"net/http"
)

// VideoStream owns a successful recording response body until the caller closes it.
type VideoStream struct {
	Body        io.ReadCloser
	ContentType string
	ContentLen  int64
	Headers     http.Header
}
