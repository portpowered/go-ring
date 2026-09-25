package ring

import "testing"

func TestRTCWebSocketURLExplicitOverrideHasStablePrecedence(t *testing.T) {
	for _, options := range [][]Option{
		{WithEndpoints(Endpoints{SignalingURL: "wss://profile.example/ws"}), WithRTCWebSocketURL("ws://explicit.example/ws")},
		{WithRTCWebSocketURL("ws://explicit.example/ws"), WithEndpoints(Endpoints{SignalingURL: "wss://profile.example/ws"})},
	} {
		client, err := NewClient(options...)
		if err != nil {
			t.Fatal(err)
		}
		if got := client.rtcWebSocketURL; got != "ws://explicit.example/ws" {
			t.Fatalf("RTC URL = %q, want explicit option", got)
		}
	}
}
