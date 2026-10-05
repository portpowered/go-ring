package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/stretchr/testify/require"
)

const cookieIsolationReplayOrigin = "https://oauth.synthetic.test"

type cookieIsolationFixture struct {
	Classification string            `json:"classification"`
	Exchanges      []replay.Exchange `json:"exchanges"`
}

type countedCookieIsolationTransport struct {
	inner *replay.Transport
	calls int
}

func (transport *countedCookieIsolationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++

	response, err := transport.inner.RoundTrip(request)
	if err != nil {
		return nil, wrapReplayTestError("cookie isolation replay transport", err)
	}

	return response, nil
}

func loadCookieIsolationExchanges(t *testing.T) []replay.Exchange {
	t.Helper()

	path := filepath.Join("fixtures", "http", "synthetic", "account-cookie-isolation.json")
	data, err := os.ReadFile(path) // #nosec G304 -- this is a fixed synthetic replay fixture.
	require.NoError(t, err)

	var fixture cookieIsolationFixture

	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Equal(t, "synthetic", fixture.Classification)
	require.Len(t, fixture.Exchanges, 2)

	for _, exchange := range fixture.Exchanges {
		require.Equal(t, cookieIsolationReplayOrigin, exchange.Request.Origin)
		require.Equal(t, replay.HeadersExact, exchange.Request.HeadersMode)
		require.NotNil(t, exchange.Request.Query)
	}

	return fixture.Exchanges
}

func TestRingHTTPClientSnapshotIsolatesAccountRefreshReplayFromLateCookieJar(t *testing.T) {
	t.Parallel()

	exchanges := loadCookieIsolationExchanges(t)
	transport := replay.NewTransport(exchanges...)
	callerHTTPClient := &http.Client{Transport: transport}
	client, err := ring.NewClient(
		ring.WithHTTPClient(callerHTTPClient),
		ring.WithEndpoints(ring.Endpoints{
			OAuthBaseURL:     cookieIsolationReplayOrigin,
			APIBaseURL:       "",
			SolutionsBaseURL: "",
			SignalingURL:     "",
		}),
	)
	require.NoError(t, err)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	oauthURL, err := url.Parse(cookieIsolationReplayOrigin)
	require.NoError(t, err)
	jar.SetCookies(oauthURL, []*http.Cookie{{Name: "account_session", Value: "first-account-secret"}})
	callerHTTPClient.Jar = jar

	accounts := []ring.RefreshTokenRequest{
		{RefreshToken: "first-refresh", HardwareID: "first-hardware"},
		{RefreshToken: "second-refresh", HardwareID: "second-hardware"},
	}
	wantAccessTokens := []string{"first-next-access", "second-next-access"}

	for index, account := range accounts {
		response, refreshErr := client.RefreshToken(context.Background(), account)
		require.NoError(t, refreshErr)
		require.Equal(t, wantAccessTokens[index], response.AccessToken)
	}

	require.NoError(t, transport.AssertConsumed())
}

func TestRESTHTTPClientCookieJarConfigurationFailsClosedDuringReplay(t *testing.T) {
	t.Parallel()

	replayTransport := replay.NewTransport()
	transport := &countedCookieIsolationTransport{inner: replayTransport, calls: 0}
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	oauthURL, err := url.Parse(cookieIsolationReplayOrigin)
	require.NoError(t, err)
	jar.SetCookies(oauthURL, []*http.Cookie{{Name: "account_session", Value: "account-secret"}})

	client := rest.NewClient(
		rest.WithHTTPClient(&http.Client{Jar: jar, Transport: transport}),
		rest.WithEndpointBases("https://api.synthetic.test", cookieIsolationReplayOrigin),
	)

	var configurationErr *rest.HTTPClientConfigurationError

	require.ErrorAs(t, client.ConfigurationError(), &configurationErr)
	require.Same(t, jar, client.HTTPClient().Jar, "the unsafe caller configuration must remain inspectable")

	_, requestErr := client.RefreshAccessTokenFor(context.Background(), "first-refresh", "first-hardware")
	require.ErrorAs(t, requestErr, &configurationErr, "request errors must preserve the typed configuration cause")
	require.Zero(t, transport.calls, "an invalid shared cookie jar must block the outbound replay request")
	require.NoError(t, replayTransport.AssertConsumed(), "the empty no-send replay must be fully consumed")
}
