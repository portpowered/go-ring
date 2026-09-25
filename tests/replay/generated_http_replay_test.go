package replay_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/stretchr/testify/require"
)

// The generated client must decode the same recorded payload used by the
// domain client. This catches schema drift without contacting Ring.
func TestGeneratedHTTPDeviceListReplay(t *testing.T) {
	const origin = "https://api.ring.com"
	transport := replay.NewTransport(deviceListExchange(t, origin))
	client, err := generatedhttp.NewClientWithResponses(origin, generatedhttp.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)

	response, err := client.ListDevicesWithResponse(context.Background(), func(_ context.Context, request *http.Request) error {
		request.Header.Set("Accept", "application/json")
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)
	require.NotEmpty(t, response.JSON200.Devices)
	require.Equal(t, "stickup_cam_mini_ptz_v1", response.JSON200.Devices[0].Kind)
	require.NotNil(t, response.JSON200.Devices[0].Health)
	require.NoError(t, transport.AssertConsumed())
}
