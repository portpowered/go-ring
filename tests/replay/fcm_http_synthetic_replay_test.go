package replay_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/dependencies/push"
	pushreceiver "github.com/portpowered/go-ring/third_party/go-push-receiver"
	"github.com/stretchr/testify/require"
)

const syntheticFCMFID = "cAAAAAAAAAAAAAAAAAAAAAA="

const syntheticFCMP256DHKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" +
	"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

type fcmReplayNormalizer struct {
	next http.RoundTripper
}

func (normalizer fcmReplayNormalizer) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, ringerrors.NewNetworkError("read synthetic FCM request", err)
	}

	switch request.URL.Host {
	case protocol.FCMInstallationsHost:
		var fields map[string]any

		err := json.Unmarshal(body, &fields)
		if err != nil {
			return nil, ringerrors.NewBadRequestError("decode synthetic installation request", err)
		}

		fields[protocol.FCMInstallationsFIDKey] = syntheticFCMFID

		body, err = json.Marshal(fields)
		if err != nil {
			return nil, ringerrors.NewInternalServerError("encode synthetic installation request", err)
		}
	case protocol.FCMRegistrationsHost:
		var fields map[string]map[string]string

		err := json.Unmarshal(body, &fields)
		if err != nil {
			return nil, ringerrors.NewBadRequestError("decode synthetic registration request", err)
		}

		web := fields[protocol.FCMRegistrationWebKey]
		web[protocol.FCMRegistrationP256DHKey] = syntheticFCMP256DHKey
		web[protocol.FCMRegistrationAuthKey] = "AAAAAAAAAAAAAAAAAAAAAA=="

		body, err = json.Marshal(fields)
		if err != nil {
			return nil, ringerrors.NewInternalServerError("encode synthetic registration request", err)
		}
	}

	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))

	response, err := normalizer.next.RoundTrip(request)
	if err != nil {
		return response, ringerrors.NewNetworkError("replay synthetic FCM request to "+request.URL.Host, err)
	}

	return response, nil
}

func TestFCMHTTPSetupUsesPairedSyntheticReplay(t *testing.T) {
	t.Parallel()

	fixtureNames := []string{
		"checkin",
		"legacy-registration",
		"installation",
		"registration",
	}

	exchanges := make([]replay.Exchange, 0, len(fixtureNames))

	for _, name := range fixtureNames {
		fixturePath := filepath.Join("fixtures", "http", "synthetic", "fcm", name+".json")
		exchange, err := replay.LoadExchange(fixturePath)
		require.NoError(t, err)

		exchanges = append(exchanges, exchange)
	}

	strictReplay := replay.NewTransport(exchanges...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

	defer cancel()

	type dialCall struct{ network, address string }

	dialCalls := make(chan dialCall, 2)
	dialOffline := push.DialContextFunc(func(_ context.Context, network, address string) (net.Conn, error) {
		dialCalls <- dialCall{network: network, address: address}

		return nil, ringerrors.NewNetworkError("synthetic replay keeps MCS offline", net.ErrClosed)
	})

	events, err := push.StartWithTransports(ctx, nil, fcmReplayNormalizer{next: strictReplay}, dialOffline)
	require.NoError(t, err)

	credentials := waitForFCMCredentials(t, ctx, cancel, events)

	for range events {
	}

	require.Equal(t, "synthetic-fcm-token", credentials.Token)

	select {
	case call := <-dialCalls:
		require.Equal(t, protocol.MCSNetwork, call.network)
		require.Equal(t, net.JoinHostPort(protocol.MCSHost, protocol.MCSPort), call.address)
	case <-time.After(3 * time.Second):
		t.Fatal("FCM registration replay did not reach its injected offline MCS dial")
	}

	select {
	case call := <-dialCalls:
		t.Fatalf("unexpected repeated MCS dial: %#v", call)
	default:
	}

	require.NoError(t, strictReplay.AssertConsumed())

	duplicateRequest := requestFromExchange(t, exchanges[0])
	duplicateResponse, err := strictReplay.RoundTrip(duplicateRequest)
	require.Error(t, err, "a consumed FCM operation must not be replayed twice")

	if duplicateResponse != nil {
		require.NoError(t, duplicateResponse.Body.Close())
	}

	require.Error(t, strictReplay.AssertConsumed())

	unlistedReplay := replay.NewTransport()
	unlistedRequest, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://android.clients.google.com/unlisted",
		bytes.NewReader(nil),
	)
	require.NoError(t, err)
	unlistedResponse, err := unlistedReplay.RoundTrip(unlistedRequest)
	require.Error(t, err, "an unlisted FCM operation must not receive a fallback response")

	if unlistedResponse != nil {
		require.NoError(t, unlistedResponse.Body.Close())
	}

	require.Error(t, unlistedReplay.AssertConsumed())
}

func waitForFCMCredentials(
	t *testing.T,
	ctx context.Context,
	cancel context.CancelFunc,
	events <-chan push.Event,
) pushreceiver.FCMCredentials {
	t.Helper()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("FCM registration ended without credentials")
			}

			switch event.Kind {
			case push.KindCredentials:
				var credentials pushreceiver.FCMCredentials

				err := json.Unmarshal(event.Credentials, &credentials)
				require.NoError(t, err)
				cancel()

				return credentials
			case push.KindRetry, push.KindClosed:
				t.Fatalf("FCM setup failed before receiving credentials: %v", event.Err)
			case push.KindConnected, push.KindMessage:
			}
		case <-ctx.Done():
			t.Fatal("FCM registration replay timed out")
		}
	}
}

func requestFromExchange(t *testing.T, exchange replay.Exchange) *http.Request {
	t.Helper()

	requestURL, err := url.Parse(exchange.Request.Origin + exchange.Request.Path)
	require.NoError(t, err)

	body := []byte(nil)

	if exchange.Request.BodyEncoding == "base64" {
		var encoded string

		require.NoError(t, json.Unmarshal(exchange.Request.Body, &encoded))
		body, err = base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
	}

	request, err := http.NewRequestWithContext(
		context.Background(),
		exchange.Request.Method,
		requestURL.String(),
		bytes.NewReader(body),
	)
	require.NoError(t, err)

	request.Header = exchange.Request.Headers.Clone()

	return request
}
