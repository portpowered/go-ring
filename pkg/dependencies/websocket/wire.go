package websocket

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/portpowered/go-ring/internal/generatedsignaling"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
)

func unmarshalSignalingFrame(encoded []byte) (signaling.Message, error) {
	var discriminator generatedsignaling.SignalingInboundDiscriminator

	err := json.Unmarshal(encoded, &discriminator)
	if err != nil {
		return signaling.Message{}, wrapSignalingWireError(err, "decode generated signaling discriminator")
	}

	if discriminator.Method == nil || discriminator.Body == nil {
		return signaling.Message{}, signalingWireError("signaling frame requires a recognized method and body")
	}

	fields, err := decodeEnvelopeFields(encoded)
	if err != nil {
		return signaling.Message{}, err
	}

	methodValue, ok := discriminator.Method.Value().(string)
	if !ok || methodValue != fields.method {
		return signaling.Message{}, signalingWireError("unknown signaling method %q", fields.method)
	}

	if fields.dialogID == "" || fields.body == nil {
		return signaling.Message{}, signalingWireError("signaling frame requires dialog_id and body")
	}

	bodyFields, err := decodeBodyFields(fields.body)
	if err != nil {
		return signaling.Message{}, err
	}

	err = validateInboundFrame(encoded, fields, bodyFields)
	if err != nil {
		return signaling.Message{}, err
	}

	return signaling.Message{Method: fields.method, DialogID: fields.dialogID, RIID: fields.riid, Body: fields.body}, nil
}

type signalingEnvelopeFields struct {
	method   string
	dialogID string
	riid     string
	body     json.RawMessage
}

func decodeEnvelopeFields(encoded []byte) (signalingEnvelopeFields, error) {
	var values map[string]json.RawMessage

	err := json.Unmarshal(encoded, &values)
	if err != nil {
		return signalingEnvelopeFields{}, wrapSignalingWireError(err, "decode signaling envelope fields")
	}

	if values == nil {
		return signalingEnvelopeFields{}, signalingWireError("signaling envelope must be an object")
	}

	var result signalingEnvelopeFields

	fields := map[string]*string{
		"method":    &result.method,
		"dialog_id": &result.dialogID,
		"riid":      &result.riid,
	}
	for name, destination := range fields {
		if raw, exists := values[name]; exists {
			err := json.Unmarshal(raw, destination)
			if err != nil {
				return signalingEnvelopeFields{}, wrapSignalingWireError(err, "decode signaling envelope %s", name)
			}
		}
	}

	result.body = values["body"]

	return result, nil
}

func decodeBodyFields(encoded json.RawMessage) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage

	err := json.Unmarshal(encoded, &fields)
	if err != nil {
		return nil, wrapSignalingWireError(err, "decode signaling body")
	}

	if fields == nil {
		return nil, signalingWireError("signaling body must be an object")
	}

	return fields, nil
}

func validateInboundFrame(encoded []byte, envelope signalingEnvelopeFields, body map[string]json.RawMessage) error {
	switch envelope.method {
	case protocol.MethodCameraStarted:
		return validateTypedInbound[generatedsignaling.ServerCameraStartedFrame](encoded, envelope, body,
			func(frame *generatedsignaling.ServerCameraStartedFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "doorbot_id", "session_id")
	case protocol.MethodClose:
		return validateTypedInbound[generatedsignaling.ServerCloseFrame](encoded, envelope, body,
			func(frame *generatedsignaling.ServerCloseFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			})
	case protocol.MethodICE:
		if _, hasMID := body["mid"]; hasMID {
			return validateTypedInbound[generatedsignaling.LiveIceFrame](encoded, envelope, body,
				func(frame *generatedsignaling.LiveIceFrame) (string, string, bool) {
					return frame.Method, frame.DialogId, frame.Body != nil
				}, "doorbot_id", "ice", "mid", "mlineindex")
		}

		return validateTypedInbound[generatedsignaling.ServerIceFrame](encoded, envelope, body,
			func(frame *generatedsignaling.ServerIceFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "ice", "mlineindex")
	case protocol.MethodNotification:
		return validateTypedInbound[generatedsignaling.SessionNotificationFrame](encoded, envelope, body,
			func(frame *generatedsignaling.SessionNotificationFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "doorbot_id", "session_id", "is_ok", "text")
	case protocol.MethodPong:
		return validateTypedInbound[generatedsignaling.SessionPongFrame](encoded, envelope, body,
			func(frame *generatedsignaling.SessionPongFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "doorbot_id", "session_id")
	case protocol.MethodPushEvent:
		return validateTypedInbound[generatedsignaling.PushEventFrame](encoded, envelope, body,
			func(frame *generatedsignaling.PushEventFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "notification_scope", "notification_type", "payload", "subscription_id")
	case protocol.MethodPushSubscriptionAck:
		return validateTypedInbound[generatedsignaling.PushSubscriptionAckFrame](encoded, envelope, body,
			func(frame *generatedsignaling.PushSubscriptionAckFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "status", "subscription_id")
	case protocol.MethodRPC:
		return validateTypedInbound[generatedsignaling.ServerRpcFrame](encoded, envelope, body,
			func(frame *generatedsignaling.ServerRpcFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "command")
	case protocol.MethodSDP:
		if hasSessionIdentifier(body["session_info"]) {
			return validateTypedInbound[generatedsignaling.LiveAnswerFrame](encoded, envelope, body,
				func(frame *generatedsignaling.LiveAnswerFrame) (string, string, bool) {
					return frame.Method, frame.DialogId, frame.Body != nil
				}, "doorbot_id", "session_id", "sdp", "session_info", "type")
		}

		return validateTypedInbound[generatedsignaling.PlaybackAnswerFrame](encoded, envelope, body,
			func(frame *generatedsignaling.PlaybackAnswerFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "doorbot_id", "session_id", "sdp", "type")
	case protocol.MethodSessionCreated:
		return validateTypedInbound[generatedsignaling.SessionCreatedFrame](encoded, envelope, body,
			func(frame *generatedsignaling.SessionCreatedFrame) (string, string, bool) {
				return frame.Method, frame.DialogId, frame.Body != nil
			}, "doorbot_id", "session_id")
	default:
		return signalingWireError("unsupported inbound signaling method %q", envelope.method)
	}
}

func validateTypedInbound[Frame any](
	encoded []byte,
	envelope signalingEnvelopeFields,
	body map[string]json.RawMessage,
	metadata func(*Frame) (string, string, bool),
	required ...string,
) error {
	decodeInput := encoded

	if envelope.method == protocol.MethodClose {
		normalized, err := normalizeCloseReasonCode(encoded)
		if err != nil {
			return err
		}

		decodeInput = normalized
	}

	var frame Frame

	err := json.Unmarshal(decodeInput, &frame)
	if err != nil {
		return wrapSignalingWireError(err, "decode generated signaling frame for %q", envelope.method)
	}

	method, dialogID, hasBody := metadata(&frame)
	if method != envelope.method || dialogID != envelope.dialogID || !hasBody {
		return signalingWireError("signaling method %q does not match its generated frame shape", envelope.method)
	}

	for _, name := range required {
		if _, exists := body[name]; !exists {
			return signalingWireError("signaling body for %q is missing required field %q", envelope.method, name)
		}
	}

	return nil
}

func hasSessionIdentifier(encoded json.RawMessage) bool {
	if len(encoded) == 0 {
		return false
	}

	fields, err := decodeBodyFields(encoded)
	if err != nil {
		return false
	}

	_, exists := fields["session_id"]

	return exists
}

func marshalSignalingFrame(message signaling.Message) ([]byte, error) {
	switch message.Method {
	case protocol.MethodActivateSession:
		return marshalActivateSessionFrame(message)
	case protocol.MethodClose:
		return marshalCloseFrame(message)
	case protocol.MethodICE:
		return marshalICEFrame(message)
	case protocol.MethodLiveView:
		return marshalLiveViewFrame(message)
	case protocol.MethodMicEnable:
		return marshalMicEnableFrame(message)
	case protocol.MethodPing:
		return marshalPingFrame(message)
	case protocol.MethodPlayback:
		return marshalPlaybackFrame(message)
	case protocol.MethodPushHeartbeat:
		return marshalPushHeartbeatFrame(message)
	case protocol.MethodPushSubscribe:
		return marshalPushSubscribeFrame(message)
	case protocol.MethodPushUnsubscribe:
		return marshalPushUnsubscribeFrame(message)
	case protocol.MethodRPC:
		return marshalRPCFrame(message)
	case protocol.MethodStreamOptions:
		return marshalStreamOptions(message)
	default:
		return nil, signalingWireError("unknown or unsupported outbound signaling method %q", message.Method)
	}
}

func marshalActivateSessionFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodActivateSession,
		[]string{"doorbot_id", "session_id"},
		func(body *generatedsignaling.SessionBody) any {
			return generatedsignaling.SessionActivateFrame{
				Method: protocol.MethodActivateSession, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalCloseFrame(message signaling.Message) ([]byte, error) {
	if bodyHasProperty(message.Body, "reason") {
		return marshalGeneratedFrame(
			message,
			protocol.MethodClose,
			[]string{"doorbot_id", "session_id", "reason"},
			func(body *generatedsignaling.PlaybackCloseBody) any {
				return generatedsignaling.PlaybackCloseFrame{
					Method: protocol.MethodClose, DialogId: message.DialogID,
					Riid: message.RIID, Body: body, AdditionalProperties: nil,
				}
			},
		)
	}

	return marshalGeneratedFrame(
		message,
		protocol.MethodClose,
		[]string{"doorbot_id", "session_id"},
		func(body *generatedsignaling.SessionBody) any {
			return generatedsignaling.SessionCloseFrame{
				Method: protocol.MethodClose, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalICEFrame(message signaling.Message) ([]byte, error) {
	if bodyHasProperty(message.Body, "mid") {
		return marshalGeneratedFrame(
			message,
			protocol.MethodICE,
			[]string{"doorbot_id", "ice", "mid", "mlineindex"},
			func(body *generatedsignaling.LiveIceBody) any {
				return generatedsignaling.LiveIceFrame{
					Method: protocol.MethodICE, DialogId: message.DialogID,
					Riid: message.RIID, Body: body, AdditionalProperties: nil,
				}
			},
		)
	}

	return marshalGeneratedFrame(
		message,
		protocol.MethodICE,
		[]string{"doorbot_id", "session_id", "ice", "mlineindex"},
		func(body *generatedsignaling.IceCandidateBody) any {
			return generatedsignaling.IceCandidateFrame{
				Method: protocol.MethodICE, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalLiveViewFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodLiveView,
		[]string{"doorbot_id", "sdp", "stream_options", "type"},
		func(body *generatedsignaling.LiveViewBody) any {
			return generatedsignaling.LiveViewFrame{
				Method: protocol.MethodLiveView, DialogId: message.DialogID,
				Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalMicEnableFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodMicEnable,
		[]string{"doorbot_id", "session_id", "enabled"},
		func(body *generatedsignaling.SessionMicrophoneBody) any {
			return generatedsignaling.SessionMicrophoneFrame{
				Method: protocol.MethodMicEnable, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalPingFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodPing,
		[]string{"doorbot_id", "session_id"},
		func(body *generatedsignaling.SessionBody) any {
			return generatedsignaling.SessionPingFrame{
				Method: protocol.MethodPing, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalPlaybackFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodPlayback,
		[]string{"doorbot_id", "entry_point", "sdp", "type"},
		func(body *generatedsignaling.PlaybackOfferBody) any {
			return generatedsignaling.PlaybackOfferFrame{
				Method: protocol.MethodPlayback, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalPushHeartbeatFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodPushHeartbeat,
		[]string{"subscription_id"},
		func(body *generatedsignaling.PushSubscriptionBody) any {
			return generatedsignaling.PushHeartbeatFrame{
				Method: protocol.MethodPushHeartbeat, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalPushSubscribeFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodPushSubscribe,
		[]string{"requested_notifications"},
		func(body *generatedsignaling.PushSubscribeBody) any {
			return generatedsignaling.PushSubscribeFrame{
				Method: protocol.MethodPushSubscribe, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalPushUnsubscribeFrame(message signaling.Message) ([]byte, error) {
	return marshalGeneratedFrame(
		message,
		protocol.MethodPushUnsubscribe,
		[]string{"subscription_id"},
		func(body *generatedsignaling.PushSubscriptionBody) any {
			return generatedsignaling.PushUnsubscribeFrame{
				Method: protocol.MethodPushUnsubscribe, DialogId: message.DialogID,
				Riid: message.RIID, Body: body, AdditionalProperties: nil,
			}
		},
	)
}

func marshalRPCFrame(message signaling.Message) ([]byte, error) {
	commandMethod, err := rpcCommandMethod(message.Body)
	if err != nil {
		return nil, err
	}

	switch commandMethod {
	case protocol.RPCPanContinuous, protocol.RPCTiltContinuous:
		err = validateRPCFields(
			message.Body,
			[]string{"jsonrpc", "id", "method", "params"},
			[]string{"sessionId", "timestamp", "version", "direction", "speed"},
		)
		if err != nil {
			return nil, err
		}

		return marshalGeneratedFrame(
			message,
			protocol.MethodRPC,
			[]string{"doorbot_id", "session_id", "command"},
			func(body *generatedsignaling.PtzContinuousCommandBody) any {
				return generatedsignaling.PtzContinuousCommandFrame{
					Method: protocol.MethodRPC, DialogId: message.DialogID,
					Riid: message.RIID, Body: body, AdditionalProperties: nil,
				}
			},
		)
	case protocol.RPCPanStep, protocol.RPCTiltStep:
		err = validateRPCFields(
			message.Body,
			[]string{"jsonrpc", "id", "method", "params"},
			[]string{"sessionId", "timestamp", "version", "direction"},
		)
		if err != nil {
			return nil, err
		}

		return marshalGeneratedFrame(
			message,
			protocol.MethodRPC,
			[]string{"doorbot_id", "session_id", "command"},
			func(body *generatedsignaling.PtzCommandBody) any {
				return generatedsignaling.PtzCommandFrame{
					Method: protocol.MethodRPC, DialogId: message.DialogID,
					Riid: message.RIID, Body: body, AdditionalProperties: nil,
				}
			},
		)
	default:
		return nil, signalingWireError("unsupported PTZ RPC method %q", commandMethod)
	}
}

func rpcCommandMethod(body json.RawMessage) (string, error) {
	fields, err := decodeBodyFields(body)
	if err != nil {
		return "", err
	}

	commandRaw, exists := fields["command"]
	if !exists {
		return "", signalingWireError("PTZ RPC body is missing command")
	}

	command, err := decodeBodyFields(commandRaw)
	if err != nil {
		return "", err
	}

	methodRaw, exists := command["method"]
	if !exists {
		return "", signalingWireError("PTZ RPC command is missing method")
	}

	var method string

	err = json.Unmarshal(methodRaw, &method)
	if err != nil {
		return "", wrapSignalingWireError(err, "decode PTZ RPC method")
	}

	return method, nil
}

func validateRPCFields(body json.RawMessage, commandRequired, paramsRequired []string) error {
	fields, err := decodeBodyFields(body)
	if err != nil {
		return err
	}

	command, err := decodeBodyProperty(fields, "command")
	if err != nil {
		return err
	}

	err = requireJSONFields(command, commandRequired...)
	if err != nil {
		return wrapSignalingWireError(err, "PTZ RPC command")
	}

	params, err := decodeBodyProperty(command, "params")
	if err != nil {
		return err
	}

	err = requireJSONFields(params, paramsRequired...)
	if err != nil {
		return wrapSignalingWireError(err, "PTZ RPC params")
	}

	return nil
}

func decodeBodyProperty(fields map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	encoded, exists := fields[key]
	if !exists {
		return nil, signalingWireError("missing required field %q", key)
	}

	decoded, err := decodeBodyFields(encoded)
	if err != nil {
		return nil, wrapSignalingWireError(err, "decode field %q", key)
	}

	return decoded, nil
}

func requireJSONFields(fields map[string]json.RawMessage, required ...string) error {
	for _, key := range required {
		if _, exists := fields[key]; !exists {
			return signalingWireError("missing required field %q", key)
		}
	}

	return nil
}

func marshalStreamOptions(message signaling.Message) ([]byte, error) {
	hasAudio := bodyHasProperty(message.Body, "audio_enabled")
	hasVideo := bodyHasProperty(message.Body, "video_enabled")

	switch {
	case hasAudio && hasVideo:
		return marshalGeneratedFrame(
			message,
			protocol.MethodStreamOptions,
			[]string{"doorbot_id", "session_id", "audio_enabled", "video_enabled"},
			func(body *generatedsignaling.SessionStreamAudioVideoOptionsBody) any {
				return generatedsignaling.SessionStreamAudioVideoOptionsFrame{
					Method: protocol.MethodStreamOptions, DialogId: message.DialogID,
					Riid: message.RIID, Body: body, AdditionalProperties: nil,
				}
			},
		)
	case hasAudio:
		return marshalGeneratedFrame(
			message,
			protocol.MethodStreamOptions,
			[]string{"doorbot_id", "session_id", "audio_enabled"},
			func(body *generatedsignaling.SessionStreamAudioOptionsBody) any {
				return generatedsignaling.SessionStreamAudioOptionsFrame{
					Method: protocol.MethodStreamOptions, DialogId: message.DialogID,
					Riid: message.RIID, Body: body, AdditionalProperties: nil,
				}
			},
		)
	case hasVideo:
		return marshalGeneratedFrame(
			message,
			protocol.MethodStreamOptions,
			[]string{"doorbot_id", "session_id", "video_enabled"},
			func(body *generatedsignaling.SessionStreamVideoOptionsBody) any {
				return generatedsignaling.SessionStreamVideoOptionsFrame{
					Method: protocol.MethodStreamOptions, DialogId: message.DialogID,
					Riid: message.RIID, Body: body, AdditionalProperties: nil,
				}
			},
		)
	default:
		return nil, signalingWireError("stream_options requires audio_enabled, video_enabled, or both")
	}
}

func marshalGeneratedFrame[Body any](
	message signaling.Message,
	method string,
	required []string,
	build func(*Body) any,
) ([]byte, error) {
	if message.Method != method || message.DialogID == "" {
		return nil, signalingWireError("signaling frame method and dialog_id are required")
	}

	if len(bytes.TrimSpace(message.Body)) == 0 || bytes.Equal(bytes.TrimSpace(message.Body), []byte("null")) {
		return nil, signalingWireError("signaling frame body is required")
	}

	var fields map[string]json.RawMessage

	err := json.Unmarshal(message.Body, &fields)
	if err != nil {
		return nil, wrapSignalingWireError(err, "decode signaling body fields")
	}

	if fields == nil {
		return nil, signalingWireError("signaling body must be an object")
	}

	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return nil, signalingWireError("signaling body is missing required field %q", name)
		}
	}

	var body Body

	err = json.Unmarshal(message.Body, &body)
	if err != nil {
		return nil, wrapSignalingWireError(err, "decode generated signaling body")
	}

	encoded, err := json.Marshal(build(&body))
	if err != nil {
		return nil, wrapSignalingWireError(err, "encode generated signaling frame")
	}

	return encoded, nil
}

func signalingWireError(format string, values ...any) error {
	return ringerrors.NewBadRequestError(fmt.Sprintf(format, values...), nil)
}

func wrapSignalingWireError(cause error, format string, values ...any) error {
	return ringerrors.NewBadRequestError(fmt.Sprintf(format, values...), cause)
}

func bodyHasProperty(body json.RawMessage, key string) bool {
	var fields map[string]json.RawMessage

	return json.Unmarshal(body, &fields) == nil && fields != nil && fields[key] != nil
}
