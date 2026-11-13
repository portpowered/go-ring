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

// registerSession registers the test account's hardware ID; the client's own error is the subject.
func registerSession(client *ring.Client) error {
	//nolint:wrapcheck // The tests assert the client's own error type.
	return client.RegisterSession(
		context.Background(),
		ring.RegisterSessionRequest{Auth: ring.AuthContext{AccessToken: "token-1", HardwareID: "hardware-abc"}},
	)
}

func TestClientOperationsNeverRegisterASession(t *testing.T) {
	t.Parallel()

	client, server := newRegistrationClient(t)

	for range 20 {
		requireListed(t, client, "token-1")
	}

	require.Zero(t, server.count("token-1"), "registration is the caller's explicit operation")
}

func TestRegisterSessionRegistersOnEveryCall(t *testing.T) {
	t.Parallel()

	client, server := newRegistrationClient(t)

	require.NoError(t, registerSession(client))
	require.NoError(t, registerSession(client))
	require.Equal(t, 2, server.count("token-1"), "the client keeps no record of registrations")
}

func TestThrottledRegistrationIsARateLimit(t *testing.T) {
	t.Parallel()

	client, server := newRegistrationClient(t, http.StatusNotAcceptable)

	err := registerSession(client)
	require.True(t, ringapimodels.IsRateLimitError(err), "406 from registration is a rate limit: %v", err)

	require.NoError(t, registerSession(client))
	require.Equal(t, 2, server.count("token-1"))
}
