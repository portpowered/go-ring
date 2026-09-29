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

func TestRESTBaseURIOptionUsesPairedPushRegistration(t *testing.T) {
	t.Parallel()

	exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "ring-push-register.json"))
	require.NoError(t, err)

	transport := replay.NewTransport(exchange)
	client := rest.NewClient(
		rest.WithHTTPClient(&http.Client{Transport: transport}),
		rest.WithBaseURI("https://api.ring.com"),
	)
	ctx := requestauth.WithAccount(context.Background(), requestauth.Account{
		AccessToken: "synthetic-access-token",
		HardwareID:  "",
	})

	require.NoError(t, client.RegisterPushDevice(ctx, "synthetic-fcm-token"))
	require.NoError(t, transport.AssertConsumed())
}

func TestRESTBaseURIRejectsMalformedURLBeforeTransport(t *testing.T) {
	t.Parallel()

	client := rest.NewClient(rest.WithBaseURI("http://%zz"))
	ctx := requestauth.WithAccount(context.Background(), requestauth.Account{
		AccessToken: "synthetic-access-token",
		HardwareID:  "",
	})

	_, err := client.GetDevices(ctx)
	require.True(t, ringapimodels.IsNetworkError(err))
}

func TestRESTBaseURIOptionPreservesPairedPathPrefix(t *testing.T) {
	t.Parallel()

	exchange, err := replay.LoadExchange(filepath.Join(
		"fixtures", "http", "synthetic", "ring-push-register-base-path.json",
	))
	require.NoError(t, err)

	transport := replay.NewTransport(exchange)
	client := rest.NewClient(
		rest.WithHTTPClient(&http.Client{Transport: transport}),
		rest.WithBaseURI("https://api.ring.com/synthetic-prefix"),
	)
	ctx := requestauth.WithAccount(context.Background(), requestauth.Account{
		AccessToken: "synthetic-access-token",
		HardwareID:  "",
	})

	require.NoError(t, client.RegisterPushDevice(ctx, "synthetic-fcm-token"))
	require.NoError(t, transport.AssertConsumed())
}
