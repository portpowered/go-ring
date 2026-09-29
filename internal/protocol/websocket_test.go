package protocol_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
)

func TestValidateWebSocketURL(t *testing.T) {
	t.Parallel()

	good := strings.Replace(protocol.SignalingURL, "{client_id}", "id", 1)
	good = strings.Replace(good, "{token}", "ticket", 1)

	err := protocol.ValidateWebSocketURL(protocol.SignalingChannel, good)
	if err != nil {
		t.Fatal(err)
	}

	err = protocol.ValidateWebSocketURL(protocol.AccountEventsChannel, protocol.ExperimentalEventWebSocketURL)
	if err != nil {
		t.Fatal(err)
	}

	for name, rawURL := range map[string]string{
		"wrong path":      strings.Replace(good, "/ws?", "/other?", 1),
		"wrong version":   strings.Replace(good, "api_version=4.0", "api_version=5.0", 1),
		"missing token":   strings.Replace(good, "&token=ticket", "", 1),
		"duplicate token": good + "&token=two",
		"extra query":     good + "&extra=1",
		"wrong origin":    strings.Replace(good, "api.prod.signalling.ring.devices.a2z.com", "example.invalid", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := protocol.ValidateWebSocketURL(protocol.SignalingChannel, rawURL)
			if err == nil {
				t.Fatal("invalid signaling endpoint passed validation")
			}
		})
	}

	err = protocol.ValidateWebSocketURL(
		protocol.AccountEventsChannel, "wss://api.ring.com/clients_api/ws?unexpected=1",
	)
	if err == nil {
		t.Fatal("unexpected account event query passed validation")
	}

	err = protocol.ValidateWebSocketURL("unknown", good)
	if err == nil || !strings.Contains(err.Error(), "unknown AsyncAPI") {
		t.Fatalf("unknown channel should have a typed validation error: %v", err)
	}

	err = protocol.ValidateWebSocketURL(protocol.SignalingChannel, "wss://%zz")
	if err == nil || errors.Unwrap(err) == nil {
		t.Fatalf("malformed default URL should preserve the parser cause: %v", err)
	}
}

func TestValidateWebSocketOverride(t *testing.T) {
	t.Parallel()

	err := protocol.ValidateWebSocketOverride("ws://127.0.0.1:8080/custom?ticket=x")
	if err != nil {
		t.Fatal(err)
	}

	for _, rawURL := range []string{
		"http://127.0.0.1/ws",
		"ws://user:password@127.0.0.1/ws",
		"ws://127.0.0.1/ws#fragment",
		"/ws",
	} {
		err := protocol.ValidateWebSocketOverride(rawURL)
		if err == nil {
			t.Fatalf("unsafe override %q passed validation", rawURL)
		}
	}

	err = protocol.ValidateWebSocketOverride("ws://127.0.0.1/%zz")
	if err == nil || errors.Unwrap(err) == nil {
		t.Fatalf("malformed URL should preserve the parser cause: %v", err)
	}
}
