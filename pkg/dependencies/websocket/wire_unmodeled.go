package websocket

import (
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/signaling"
)

// unmarshalUnmodeledFrame passes through a well-formed frame whose method go-ring does not decode
// as a typed inbound frame, so that an informational message from Ring does not end every session
// on the connection. That covers methods go-ring does not model, such as Ring's stream_info, and
// methods go-ring models only for sending, such as Ring's camera_options. The frame still needs a
// method, a dialog_id and an object body; it reaches the session that owns the dialog, and
// DeviceSession.Receive returns it with its method and raw body. It reports false for a typed
// inbound or missing method, which the typed decoding validates and reports.
func unmarshalUnmodeledFrame(encoded []byte) (signaling.Message, bool, error) {
	var none signaling.Message

	fields, err := decodeEnvelopeFields(encoded)
	if err != nil {
		return none, true, err
	}

	if fields.method == "" {
		return none, false, nil
	}

	if isTypedInboundMethod(fields.method) {
		return none, false, nil
	}

	if fields.dialogID == "" || fields.body == nil {
		return none, true, signalingWireError("unmodeled signaling method %q requires dialog_id and body", fields.method)
	}

	_, err = decodeBodyFields(fields.body)
	if err != nil {
		return none, true, err
	}

	return signaling.Message{
		Method:   fields.method,
		DialogID: fields.dialogID,
		RIID:     fields.riid,
		Body:     fields.body,
	}, true, nil
}

// isTypedInboundMethod reports whether validateInboundFrame decodes method as a typed inbound
// frame. Keep it in step with that switch.
func isTypedInboundMethod(method string) bool {
	switch method {
	case protocol.MethodCameraStarted,
		protocol.MethodClose,
		protocol.MethodICE,
		protocol.MethodNotification,
		protocol.MethodPing,
		protocol.MethodPong,
		protocol.MethodPushEvent,
		protocol.MethodPushSubscriptionAck,
		protocol.MethodRPC,
		protocol.MethodSDP,
		protocol.MethodSessionCreated:
		return true
	default:
		return false
	}
}
