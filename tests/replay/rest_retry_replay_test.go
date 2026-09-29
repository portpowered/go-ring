package replay_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/requestauth"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

type cancelAfterReplayResponse struct {
	next   http.RoundTripper
	cancel context.CancelFunc
}

func (transport cancelAfterReplayResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.next.RoundTrip(request)
	if err != nil {
		return response, ringapimodels.NewNetworkError("synthetic replay round trip failed", err)
	}

	if response.StatusCode >= http.StatusInternalServerError {
		transport.cancel()
	}

	return response, nil
}

func TestGETRetryStopsWhenPairedReplayContextIsCanceled(t *testing.T) {
	t.Parallel()

	exchange, err := replay.LoadExchange(filepath.Join(
		"fixtures", "http", "synthetic", "ring-devices-unavailable.json",
	))
	require.NoError(t, err)

	transport := replay.NewTransport(exchange)

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	client := rest.NewClient(
		rest.WithHTTPClient(&http.Client{
			Transport: cancelAfterReplayResponse{next: transport, cancel: cancel},
		}),
		rest.WithBaseURI("https://api.ring.com"),
	)
	ctx = requestauth.WithAccount(ctx, requestauth.Account{
		AccessToken: "synthetic-access-token",
		HardwareID:  "",
	})

	_, err = client.GetDevices(ctx)
	require.True(t, ringapimodels.IsNetworkError(err))
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, transport.AssertConsumed())
}
