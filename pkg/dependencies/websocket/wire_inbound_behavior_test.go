package websocket_test

import (
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
)

func TestReadSignalingAcceptsModeledServerFrames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		frame string
		want  string
	}{
		{
			name:  "camera started",
			frame: `{"method":"camera_started","dialog_id":"dialog","body":{"doorbot_id":1000,"session_id":"session"}}`,
			want:  protocol.MethodCameraStarted,
		},
		{
			name:  "playback ICE",
			frame: `{"method":"ice","dialog_id":"dialog","body":{"ice":"candidate:synthetic","mlineindex":0}}`,
			want:  protocol.MethodICE,
		},
		{
			name: "live ICE",
			frame: `{"method":"ice","dialog_id":"dialog","body":` +
				`{"doorbot_id":1000,"ice":"candidate:synthetic","mid":"0","mlineindex":0}}`,
			want: protocol.MethodICE,
		},
		{
			name: "notification",
			frame: `{"method":"notification","dialog_id":"dialog","body":` +
				`{"doorbot_id":1000,"session_id":"session","is_ok":true,"text":"accepted"}}`,
			want: protocol.MethodNotification,
		},
		{
			name:  "pong",
			frame: `{"method":"pong","dialog_id":"dialog","body":{"doorbot_id":1000,"session_id":"session"}}`,
			want:  protocol.MethodPong,
		},
		{
			name: "push event",
			frame: `{"method":"push_event","dialog_id":"dialog","body":` +
				`{"notification_scope":"event","notification_type":"shoulder_tap",` +
				`"payload":{"device_id":"1000"},"subscription_id":"subscription"}}`,
			want: protocol.MethodPushEvent,
		},
		{
			name: "push subscription acknowledgment",
			frame: `{"method":"push_subscription_ack","dialog_id":"dialog","body":` +
				`{"status":"ok","subscription_id":"subscription"}}`,
			want: protocol.MethodPushSubscriptionAck,
		},
		{
			name: "RPC result",
			frame: `{"method":"rpc","dialog_id":"dialog","body":{"command":` +
				`{"jsonrpc":"2.0","id":"command","result":{"sessionId":"control"}}}}`,
			want: protocol.MethodRPC,
		},
		{
			name: "playback SDP answer",
			frame: `{"method":"sdp","dialog_id":"dialog","body":` +
				`{"doorbot_id":1000,"session_id":"session","sdp":"synthetic-answer","type":"answer"}}`,
			want: protocol.MethodSDP,
		},
		{
			name: "live SDP answer",
			frame: `{"method":"sdp","dialog_id":"dialog","body":` +
				`{"doorbot_id":1000,"session_id":"session","sdp":"synthetic-answer",` +
				`"session_info":{"session_id":"control"},"type":"answer"}}`,
			want: protocol.MethodSDP,
		},
		{
			name:  "session created",
			frame: `{"method":"session_created","dialog_id":"dialog","body":{"doorbot_id":1000,"session_id":"session"}}`,
			want:  protocol.MethodSessionCreated,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			message, err := readOneSignalingFrame(t, testCase.frame)
			if err == nil {
				t.Fatal("ReadSignaling returned without the peer closing")
			}

			if message.Method != testCase.want {
				t.Fatalf("ReadSignaling method = %q, want %q (error: %v)", message.Method, testCase.want, err)
			}
		})
	}
}

func TestReadSignalingRejectsUnmodeledAndIncompleteFrames(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		frame string
	}{
		{
			name:  "unknown method",
			frame: `{"method":"motion_event","dialog_id":"dialog","body":{"event":"motion"}}`,
		},
		{
			name:  "missing dialog id",
			frame: `{"method":"pong","body":{"doorbot_id":1000,"session_id":"session"}}`,
		},
		{
			name:  "non-object body",
			frame: `{"method":"pong","dialog_id":"dialog","body":[]}`,
		},
		{
			name: "missing required notification field",
			frame: `{"method":"notification","dialog_id":"dialog","body":` +
				`{"doorbot_id":1000,"session_id":"session","is_ok":true}}`,
		},
		{
			name:  "invalid RPC command type",
			frame: `{"method":"rpc","dialog_id":"dialog","body":{"command":true}}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			message, err := readOneSignalingFrame(t, testCase.frame)
			if err == nil {
				t.Fatal("ReadSignaling accepted an unsupported frame")
			}

			if message.Method != "" {
				t.Fatalf("invalid frame reached route callback: %#v", message)
			}
		})
	}
}

func TestReadSignalingAcceptsCloseFramesWithoutOptionalReasonCode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		frame string
	}{
		{
			name:  "empty body",
			frame: `{"method":"close","dialog_id":"dialog","body":{}}`,
		},
		{
			name:  "reason without code",
			frame: `{"method":"close","dialog_id":"dialog","body":{"reason":{"text":"closed"}}}`,
		},
		{
			name:  "signed integer code",
			frame: `{"method":"close","dialog_id":"dialog","body":{"reason":{"code":-1,"text":"closed"}}}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			message, err := readOneSignalingFrame(t, testCase.frame)
			if err == nil {
				t.Fatal("ReadSignaling returned without the peer closing")
			}

			if message.Method != protocol.MethodClose {
				t.Fatalf("ReadSignaling method = %q, want close (error: %v)", message.Method, err)
			}
		})
	}
}

func TestReadSignalingRejectsMalformedCloseReason(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		frame string
	}{
		{name: "missing body", frame: `{"method":"close","dialog_id":"dialog"}`},
		{name: "non-object body", frame: `{"method":"close","dialog_id":"dialog","body":[]}`},
		{
			name:  "non-object reason",
			frame: `{"method":"close","dialog_id":"dialog","body":{"reason":"closed"}}`,
		},
		{
			name:  "fractional code",
			frame: `{"method":"close","dialog_id":"dialog","body":{"reason":{"code":1.5}}}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			message, err := readOneSignalingFrame(t, testCase.frame)
			if err == nil {
				t.Fatal("ReadSignaling accepted a malformed close frame")
			}

			if message.Method != "" {
				t.Fatalf("invalid frame reached route callback: %#v", message)
			}
		})
	}
}
