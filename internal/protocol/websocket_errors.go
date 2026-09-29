package protocol

const (
	reasonSignalingEndpoint = "signaling URL differs from the AsyncAPI server and channel"
	reasonEventEndpoint     = "account event URL differs from the AsyncAPI server and channel"
	reasonUnknownChannel    = "unknown AsyncAPI WebSocket channel"
	reasonInvalidURL        = "WebSocket URL must have a ws or wss origin without userinfo or fragment"
	reasonUnexpectedQuery   = "signaling URL has unexpected query parameters"
	reasonMissingQueryValue = "signaling URL requires one value per query key"
	reasonSignalingQuery    = "signaling URL query differs from the AsyncAPI binding"
)

type webSocketURLError struct {
	reason string
	cause  error
}

func (e webSocketURLError) Error() string { return e.reason }

func (e webSocketURLError) Unwrap() error { return e.cause }
