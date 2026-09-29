package replay_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

// The camera capture has no intercom; this fixture records the source client's
// request contract so the endpoint can be replayed without claiming live parity.
func TestReferenceIntercomUnlock(t *testing.T) {
	t.Parallel()

	exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "reference", "intercom-unlock.json"))
	require.NoError(t, err)
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: replay.NewTransport(exchange)}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	req := ring.DeviceIDRequest{Auth: ring.AuthContext{AccessToken: "captured-token", HardwareID: ""}, DeviceID: "1000"}
	require.NoError(t, client.UnlockIntercom(context.Background(), req))
}

func TestIntercomUnlockRejectsMissingDeviceID(t *testing.T) {
	t.Parallel()

	client, err := ring.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	err = client.UnlockIntercom(context.Background(), ring.DeviceIDRequest{DeviceID: ""})
	require.True(t, ringapimodels.IsBadRequestError(err))
}
