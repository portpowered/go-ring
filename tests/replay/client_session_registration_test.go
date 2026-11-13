package replay_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

// registrationServer counts client session registrations per bearer token and answers each with
// the next status in refusals (then 204), the way Ring throttles registration with 406.
type registrationServer struct {
	mu            sync.Mutex
	registrations map[string]int
	refusals      []int
}

func (s *registrationServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")

	if request.URL.Path == "/clients_api/session" {
		s.mu.Lock()
		s.registrations[token]++

		status := http.StatusNoContent
		if len(s.refusals) > 0 {
			status, s.refusals = s.refusals[0], s.refusals[1:]
		}

		s.mu.Unlock()

		writer.WriteHeader(status)

		return
	}

	_, _ = writer.Write([]byte(`{"devices":[]}`))
}

func (s *registrationServer) count(token string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.registrations[token]
}

func newRegistrationClient(t *testing.T, refusals ...int) (*ring.Client, *registrationServer) {
	t.Helper()

	handler := &registrationServer{mu: sync.Mutex{}, registrations: map[string]int{}, refusals: refusals}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := ring.NewClient(
		ring.WithHTTPClient(server.Client()),
		ring.WithEndpoints(ring.Endpoints{APIBaseURL: server.URL, OAuthBaseURL: "", SolutionsBaseURL: "", SignalingURL: ""}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return client, handler
}

func listDevicesRequest(token string) ring.ListDevicesRequest {
	return ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: token, HardwareID: "hardware-abc"}}
}

func requireListed(t *testing.T, client *ring.Client, token string) {
	t.Helper()

	_, err := client.ListDevices(context.Background(), listDevicesRequest(token))
	require.NoError(t, err)
}

func TestClientRegistersOncePerTokenAndAgainForANewToken(t *testing.T) {
	t.Parallel()

	client, server := newRegistrationClient(t)

	for range 20 {
		requireListed(t, client, "token-1")
	}

	require.Equal(t, 1, server.count("token-1"), "one registration serves every request with the same token")

	requireListed(t, client, "token-2")
	requireListed(t, client, "token-2")
	require.Equal(t, 1, server.count("token-2"), "a refreshed token registers once")
}

func TestThrottledRegistrationIsARateLimitAndIsRetried(t *testing.T) {
	t.Parallel()

	client, server := newRegistrationClient(t, http.StatusNotAcceptable)

	_, err := client.ListDevices(context.Background(), listDevicesRequest("token-1"))
	require.True(t, ringapimodels.IsRateLimitError(err), "406 from registration is a rate limit: %v", err)

	requireListed(t, client, "token-1")
	requireListed(t, client, "token-1")
	require.Equal(t, 2, server.count("token-1"), "a refused registration is not remembered")
}
