package ring

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func TestAccountContextRequiresRequestCredentials(t *testing.T) {
	client, err := NewClient()
	require.NoError(t, err)
	ctx := client.accountContext(context.Background(), AuthContext{AccessToken: "request-token", HardwareID: "request-hardware"})
	token, err := client.getToken(ctx)
	require.NoError(t, err)
	require.Equal(t, "request-token", token)
	require.Equal(t, "request-hardware", client.hardwareIDFor(ctx))
	_, err = client.getToken(context.Background())
	require.True(t, ringapimodels.IsTokenError(err))
	require.Empty(t, client.hardwareIDFor(context.Background()))
	_, err = client.getToken(client.accountContext(ctx, AuthContext{HardwareID: "other-hardware"}))
	require.True(t, ringapimodels.IsTokenError(err))
}

func TestLoginSessionLifecycle(t *testing.T) {
	client, err := NewClient()
	require.NoError(t, err)
	_, err = client.NewLoginSession(LoginSessionRequest{})
	require.True(t, ringapimodels.IsBadRequestError(err))
	flow, err := client.NewLoginSession(LoginSessionRequest{Username: "user@example.test", Password: "synthetic-password"})
	require.NoError(t, err)
	require.NotEmpty(t, flow.HardwareID())
	require.NoError(t, flow.Close())
	require.Error(t, flow.Request2FACode(context.Background()))
	_, err = flow.Authenticate(context.Background(), CompleteLoginRequest{})
	require.True(t, ringapimodels.IsClosedError(err))
}

func TestSharedClientRejectsCookieJar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	_, err = NewClient(WithHTTPClient(&http.Client{Jar: jar}))
	require.True(t, ringapimodels.IsBadRequestError(err))
}

func TestRefreshDoesNotBindToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		wantHardware := "refresh-hardware"
		if r.Form.Get("refresh_token") == "another-refresh" {
			wantHardware = ""
		}
		if r.Header.Get("hardware_id") != wantHardware {
			t.Errorf("refresh used another account's hardware ID")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-token","refresh_token":"new-refresh","token_type":"Bearer"}`))
	}))
	defer server.Close()
	client, err := NewClient(WithHTTPClient(server.Client()), WithEndpoints(Endpoints{OAuthBaseURL: server.URL}))
	require.NoError(t, err)
	response, err := client.RefreshToken(context.Background(), RefreshTokenRequest{RefreshToken: "old-refresh", HardwareID: "refresh-hardware"})
	require.NoError(t, err)
	require.Equal(t, "new-token", response.AccessToken)
	_, err = client.RefreshToken(context.Background(), RefreshTokenRequest{RefreshToken: "another-refresh"})
	require.NoError(t, err)
	_, err = client.RefreshToken(context.Background(), RefreshTokenRequest{})
	require.True(t, ringapimodels.IsBadRequestError(err))
	_, err = client.getToken(context.Background())
	require.True(t, ringapimodels.IsTokenError(err))
}
