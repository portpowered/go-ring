package unit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/stretchr/testify/require"
)

type endpointTransport struct{ urls []string }

func (t *endpointTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.urls = append(t.urls, req.URL.String())
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: req}, nil
}

func TestEndpointRegionsAndOverridesArePerClient(t *testing.T) {
	for _, region := range []ring.Region{ring.RegionUS, ring.RegionEU, ring.RegionFE} {
		transport := &endpointTransport{}
		client, err := ring.NewClient(ring.WithRegion(region), ring.WithHTTPClient(&http.Client{Transport: transport}))
		require.NoError(t, err)
		_, err = client.ListDevices(context.Background())
		require.NoError(t, err)
		require.Len(t, transport.urls, 1)
		require.True(t, strings.HasPrefix(transport.urls[0], "https://api.ring.com/"))
	}

	for _, options := range [][]ring.Option{
		{ring.WithRegion(ring.RegionEU), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://override.example"})},
		{ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://override.example"}), ring.WithRegion(ring.RegionEU)},
	} {
		transport := &endpointTransport{}
		client, err := ring.NewClient(append(options, ring.WithHTTPClient(&http.Client{Transport: transport}))...)
		require.NoError(t, err)
		_, err = client.ListDevices(context.Background())
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(transport.urls[0], "https://override.example/"))
	}

	defaultTransport := &endpointTransport{}
	_, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: defaultTransport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "http://[bad"}))
	require.Error(t, err)
	client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: defaultTransport}))
	require.NoError(t, err)
	_, err = client.ListDevices(context.Background())
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(defaultTransport.urls[0], "https://api.ring.com/"))
}
