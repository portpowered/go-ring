package replay_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/dependencies/push"
	pushreceiver "github.com/portpowered/go-ring/third_party/go-push-receiver"
	"github.com/stretchr/testify/require"
)

type replayRoundTripper func(*http.Request) (*http.Response, error)

func (transport replayRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestFCMRegistrationRequestNormalization(t *testing.T) {
	t.Parallel()

	publicKey := base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65))
	auth := base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 16))
	rawBody, err := json.Marshal(map[string]any{
		protocol.FCMRegistrationWebKey: map[string]string{
			protocol.FCMRegistrationVAPIDKey:    protocol.FCMDefaultVAPIDKey,
			protocol.FCMRegistrationAuthKey:     auth,
			protocol.FCMRegistrationEndpointKey: protocol.FCMRegistrationEndpointPrefix + "registration-token",
			protocol.FCMRegistrationP256DHKey:   publicKey,
		},
	})
	require.NoError(t, err)

	called := false
	transport := push.NewRegistrationTransport(replayRoundTripper(func(req *http.Request) (*http.Response, error) {
		called = true

		require.Equal(t, "installation-token", req.Header.Get("X-Goog-Firebase-Installations-Auth"))
		body, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		require.Equal(t, int64(len(body)), req.ContentLength)

		var parsed struct {
			Web map[string]json.RawMessage `json:"web"`
		}

		require.NoError(t, json.Unmarshal(body, &parsed))
		require.NotContains(t, parsed.Web, "applicationPubKey")
		require.Contains(t, parsed.Web, "auth")

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("{}")),
			Header:     make(http.Header),
		}, nil
	}))
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://fcmregistrations.googleapis.com/v1/projects/ring-17770/registrations",
		bytes.NewReader(rawBody),
	)
	require.NoError(t, err)
	req.Header.Set(protocol.FCMContentTypeHeader, protocol.FCMContentTypeJSON)
	req.Header.Set(protocol.FCMInstallationsAPIKeyHeader, protocol.FCMAPIKey)
	req.Header.Set(protocol.FCMInstallationsAuthHeader, protocol.FCMInstallationsAuthPrefix+"installation-token")
	response, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 200, response.StatusCode)
	require.True(t, called)
}

func TestFCMAdapterEventKinds(t *testing.T) {
	t.Parallel()

	credentials := &pushreceiver.FCMCredentials{Token: "fcm-test-token"}
	for _, test := range []struct {
		name string
		raw  pushreceiver.Event
		kind push.Kind
	}{
		{"credentials", &pushreceiver.UpdateCredentialsEvent{Credentials: credentials}, push.KindCredentials},
		{"connected", &pushreceiver.ConnectedEvent{}, push.KindConnected},
		{"message", &pushreceiver.MessageEvent{Data: []byte(`{"data":{}}`)}, push.KindMessage},
		{"retry", &pushreceiver.RetryEvent{ErrorObj: context.DeadlineExceeded}, push.KindRetry},
		{"unauthorized", &pushreceiver.UnauthorizedError{ErrorObj: context.Canceled}, push.KindRetry},
		{"disconnected", &pushreceiver.DisconnectedEvent{}, push.KindClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			event, ok := push.TranslateEvent(test.raw)
			require.True(t, ok)
			require.Equal(t, test.kind, event.Kind)
		})
	}

	_, ok := push.TranslateEvent(struct{}{})
	require.False(t, ok)
}

func TestFCMAdapterRejectsInvalidSavedCredentials(t *testing.T) {
	t.Parallel()

	_, err := push.Start(context.Background(), json.RawMessage(`not-json`))
	require.Error(t, err)
	_, err = push.Start(context.Background(), json.RawMessage(`{}`))
	require.Error(t, err)
}

func TestFCMAdapterCanceledBeforeConnection(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stream, err := push.Start(ctx, json.RawMessage(`{"token":"saved-token"}`))
	require.NoError(t, err)

	for range stream {
		// The canceled replay must finish without opening a live socket.
	}
}
