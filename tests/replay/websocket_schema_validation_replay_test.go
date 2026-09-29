package replay_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestDefaultWebSocketSchemasRejectEndpointMutations(t *testing.T) {
	t.Parallel()

	signalingURL := strings.Replace(protocol.SignalingURL, "{client_id}", "synthetic-client", 1)
	signalingURL = strings.Replace(signalingURL, "{token}", "synthetic-ticket", 1)
	require.NoError(t, protocol.ValidateWebSocketURL(protocol.SignalingChannel, signalingURL))
	require.NoError(t, protocol.ValidateWebSocketURL(
		protocol.AccountEventsChannel,
		protocol.ExperimentalEventWebSocketURL,
	))

	invalidSignalingURLs := []string{
		strings.Replace(signalingURL, "wss://", "ws://", 1),
		strings.Replace(signalingURL, "/ws?", "/other?", 1),
		strings.Replace(signalingURL, "api.prod.signalling.ring.devices.a2z.com", "example.invalid", 1),
		strings.Replace(signalingURL, ":443/ws?", ":444/ws?", 1),
		strings.Replace(signalingURL, "api_version=4.0", "api_version=5.0", 1),
		strings.Replace(signalingURL, "&token=synthetic-ticket", "", 1),
		signalingURL + "&extra=1",
		signalingURL + "&token=duplicate",
		strings.Replace(signalingURL, "auth_type=ring_solutions", "auth_type=unexpected", 1),
		strings.Replace(signalingURL, "client_id=ring_site-synthetic-client", "client_id=other", 1),
		strings.Replace(signalingURL, "token=synthetic-ticket", "token=", 1),
	}
	for _, rawURL := range invalidSignalingURLs {
		err := protocol.ValidateWebSocketURL(protocol.SignalingChannel, rawURL)
		require.Error(t, err)
		require.NotEmpty(t, err.Error())
	}

	err := protocol.ValidateWebSocketURL(protocol.AccountEventsChannel, protocol.ExperimentalEventWebSocketURL+"?extra=1")
	require.Error(t, err)
	require.NotEmpty(t, err.Error())

	err = protocol.ValidateWebSocketURL("unknown-channel", signalingURL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown AsyncAPI")

	err = protocol.ValidateWebSocketURL(protocol.SignalingChannel, "wss://%zz")
	require.Error(t, err)
	require.Error(t, errors.Unwrap(err))

	err = protocol.ValidateWebSocketOverride("http://events.example.invalid/ws")
	require.Error(t, err)
	require.NotEmpty(t, err.Error())
}
