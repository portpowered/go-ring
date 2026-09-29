package replay_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

type replayAccount struct {
	AccessToken string `json:"access_token"`
	HardwareID  string `json:"hardware_id"`
}

func loadReplayAccounts(t *testing.T) []replayAccount {
	t.Helper()

	data, err := os.ReadFile("fixtures/account/synthetic/account-scope.json")
	require.NoError(t, err)

	var fixture struct {
		Accounts []replayAccount `json:"accounts"`
	}

	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Len(t, fixture.Accounts, 2)

	return fixture.Accounts
}

func TestAccountRequestScopeReplay(t *testing.T) {
	t.Parallel()

	accounts := loadReplayAccounts(t)

	wantHardware := map[string]string{}

	for _, account := range accounts {
		wantHardware[account.AccessToken] = account.HardwareID
	}

	var mu sync.Mutex

	seen := map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")

		hardware := request.Header.Get("Hardware_id")

		if wantHardware[token] != hardware || token == "" {
			t.Errorf("cross-account headers: token=%q hardware=%q", token, hardware)
		}

		if request.URL.Path == legacyClientSessionPath {
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), hardware) {
				t.Errorf("registration body omits request hardware ID")
			}

			responseWriter.WriteHeader(http.StatusNoContent)

			return
		}

		if request.URL.Path != "/device_info/v3/devices" {
			http.NotFound(responseWriter, request)

			return
		}

		mu.Lock()

		seen[token]++

		mu.Unlock()

		_, _ = responseWriter.Write([]byte(`{"devices":[]}`))
	}))
	defer server.Close()

	client, err := ring.NewClient(
		ring.WithHTTPClient(server.Client()),
		ring.WithEndpoints(ring.Endpoints{APIBaseURL: server.URL}),
	)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	var group sync.WaitGroup
	for _, account := range accounts {
		group.Add(1)

		go func(account replayAccount) {
			defer group.Done()

			for range 10 {
				_, callErr := client.ListDevices(
					context.Background(),
					ring.ListDevicesRequest{
						Auth: ring.AuthContext{AccessToken: account.AccessToken, HardwareID: account.HardwareID},
					},
				)
				if callErr != nil {
					t.Error(callErr)

					return
				}
			}
		}(account)
	}

	group.Wait()
	require.Equal(t, 10, seen[accounts[0].AccessToken])
	require.Equal(t, 10, seen[accounts[1].AccessToken])

	_, err = client.ListDevices(
		context.Background(),
		ring.ListDevicesRequest{Auth: ring.AuthContext{HardwareID: "missing-token", AccessToken: ""}},
	)
	require.True(t, ringapimodels.IsTokenError(err), "missing request token: %v", err)
}

func TestLoginSessionsDoNotAuthorizeSharedClient(t *testing.T) {
	t.Parallel()

	accounts := loadReplayAccounts(t)

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != oauthTokenPath {
			http.NotFound(responseWriter, request) // select the recorded legacy fallback

			return
		}

		_ = request.ParseForm()

		for _, account := range accounts {
			if request.Form.Get("username") == account.AccessToken {
				if request.Header.Get("Hardware_id") != account.HardwareID {
					t.Errorf("login hardware mismatch")
				}

				_, _ = responseWriter.Write(
					[]byte(
						`{"access_token":"` + account.AccessToken + `","refresh_token":"refresh","token_type":"Bearer"}`,
					),
				)

				return
			}
		}

		http.Error(responseWriter, "wrong account", http.StatusBadRequest)
	}))
	defer server.Close()

	client, err := ring.NewClient(
		ring.WithHTTPClient(server.Client()),
		ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: server.URL}),
	)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	var group sync.WaitGroup
	for _, account := range accounts {
		group.Add(1)

		go func(account replayAccount) {
			defer group.Done()

			flow, flowErr := client.NewLoginSession(
				ring.LoginSessionRequest{
					Username:   account.AccessToken,
					Password:   "synthetic-password",
					HardwareID: account.HardwareID,
				},
			)
			if flowErr != nil {
				t.Error(flowErr)

				return
			}

			tokens, authErr := flow.Authenticate(context.Background(), ring.CompleteLoginRequest{})
			if authErr != nil {
				t.Error(authErr)

				return
			}

			if tokens.AccessToken != account.AccessToken {
				t.Errorf("login for %q returned another account", account.AccessToken)
			}

			closeErr := flow.Close()
			if closeErr != nil {
				t.Error(closeErr)
			}
		}(account)
	}

	group.Wait()

	_, err = client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
	require.True(t, ringapimodels.IsTokenError(err), "login leaked authorization onto shared client: %v", err)
}

func TestSignalingConnectionsBindRequestAccount(t *testing.T) {
	t.Parallel()

	accounts := loadReplayAccounts(t)

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		token := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")

		if request.URL.Path == legacyClientSessionPath {
			responseWriter.WriteHeader(http.StatusNoContent)

			return
		}

		if request.URL.Path == clapSignalingBootstrapPath {
			if token == "" {
				t.Error("ticket request has no account authorization")
			}

			_, _ = responseWriter.Write([]byte(`{"ticket":"` + token + `"}`))

			return
		}

		if request.URL.Path == "/ws" {
			valid := false
			for _, account := range accounts {
				valid = valid || (request.URL.Query().Get("ticket") == account.AccessToken &&
					request.Header.Get("Hardware_id") == account.HardwareID)
			}

			if !valid {
				t.Error("signaling ticket and hardware identity crossed accounts")
			}

			conn, err := (&websocket.Upgrader{}).Upgrade(responseWriter, request, nil)
			if err == nil {
				defer func() { _ = conn.Close() }()

				for {
					{
						_, _, err = conn.ReadMessage()
						if err != nil {
							return
						}
					}
				}
			}

			return
		}

		http.NotFound(responseWriter, request)
	}))
	defer server.Close()

	client, err := ring.NewClient(
		ring.WithHTTPClient(server.Client()),
		ring.WithEndpoints(ring.Endpoints{APIBaseURL: server.URL, SolutionsBaseURL: server.URL}),
		ring.WithSignalingWebSocketURL("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?ticket={token}"),
	)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	for _, account := range accounts {
		conn, openErr := client.OpenSignaling(
			context.Background(),
			ring.OpenSignalingRequest{
				Auth: ring.AuthContext{AccessToken: account.AccessToken, HardwareID: account.HardwareID},
			},
		)
		require.NoError(t, openErr)
		require.NoError(t, conn.Close())
	}
}

func TestEventConnectionsUseRequestAccount(t *testing.T) {
	t.Parallel()

	accounts := loadReplayAccounts(t)

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		valid := false
		for _, account := range accounts {
			valid = valid ||
				(request.Header.Get("Authorization") == "Bearer "+account.AccessToken &&
					request.Header.Get("Hardware_id") == account.HardwareID)
		}

		if !valid {
			t.Error("event socket received a different account's credentials")
		}

		conn, err := (&websocket.Upgrader{}).Upgrade(responseWriter, request, nil)
		if err != nil {
			return
		}

		defer func() { _ = conn.Close() }()

		_ = conn.WriteJSON(map[string]any{"kind": "ding", "device_id": 123})
	}))
	defer server.Close()

	client, err := ring.NewClient(ring.WithEventWebSocketURL("ws" + strings.TrimPrefix(server.URL, "http")))
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	for _, account := range accounts {
		conn, connectErr := client.ConnectEvents(
			context.Background(),
			ring.ConnectEventsRequest{
				Auth: ring.AuthContext{AccessToken: account.AccessToken, HardwareID: account.HardwareID},
			},
		)
		require.NoError(t, connectErr)

		event, receiveErr := conn.Receive()
		require.NoError(t, receiveErr)
		require.Equal(t, int64(123), event.DeviceID)
		require.NoError(t, conn.Close())
	}
}

func TestGeneratedTicketRequestUsesAccountScope(t *testing.T) {
	t.Parallel()

	account := loadReplayAccounts(t)[0]

	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+account.AccessToken ||
			r.Header.Get("Hardware_id") != account.HardwareID {
			t.Error("generated request did not receive scoped authorization")
		}

		responseWriter.Header().Set("Content-Type", "application/json")
		_, _ = responseWriter.Write([]byte(`{"ticket":"synthetic-ticket"}`))
	}))
	defer server.Close()

	client, err := ring.NewClient(
		ring.WithHTTPClient(server.Client()),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: server.URL}),
	)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	ticket, err := client.GetCapturedTickets(
		context.Background(),
		ring.GetCapturedTicketsRequest{
			Auth: ring.AuthContext{AccessToken: account.AccessToken, HardwareID: account.HardwareID},
		},
	)
	require.NoError(t, err)
	require.Equal(t, "synthetic-ticket", ticket.Ticket)
}

func TestConcurrentPKCELoginSessionsKeepChallengesSeparate(t *testing.T) {
	t.Parallel()

	accounts := loadReplayAccounts(t)

	handler := &pkceReplayHandler{t: t, accounts: accounts, mu: sync.Mutex{}, states: make(map[string]string)}
	server := httptest.NewServer(handler)

	defer server.Close()

	client, err := ring.NewClient(
		ring.WithHTTPClient(server.Client()),
		ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: server.URL}),
	)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	var group sync.WaitGroup
	for _, account := range accounts {
		group.Add(1)

		go func(account replayAccount) {
			defer group.Done()

			flow, flowErr := client.NewLoginSession(
				ring.LoginSessionRequest{
					Username:   account.AccessToken,
					Password:   "synthetic",
					HardwareID: account.HardwareID,
				},
			)
			if flowErr != nil {
				t.Error(flowErr)

				return
			}

			defer func() { _ = flow.Close() }()

			{
				err := flow.Request2FACode(context.Background())
				if err != nil {
					t.Error(err)

					return
				}
			}

			tokens, err := flow.Authenticate(context.Background(), ring.CompleteLoginRequest{OTPCode: "123456"})
			if err != nil {
				t.Error(err)

				return
			}

			if tokens.AccessToken != account.HardwareID {
				t.Errorf("login returned another account's token")
			}
		}(account)
	}

	group.Wait()
}

type pkceReplayHandler struct {
	t        *testing.T
	accounts []replayAccount
	mu       sync.Mutex
	states   map[string]string
}

func (handler *pkceReplayHandler) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case oauthAuthorizePath:
		handler.authorize(responseWriter, request)
	case oauthSignInPath:
		handler.signIn(responseWriter, request)
	case oauthTwoFactorPath:
		handler.twoFactor(responseWriter, request)
	case oauthTokenPath:
		handler.token(responseWriter, request)
	default:
		http.NotFound(responseWriter, request)
	}
}

func (handler *pkceReplayHandler) authorize(responseWriter http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("response_type") == "code" {
		hardware := request.URL.Query().Get("hardware_id")

		handler.mu.Lock()
		handler.states[hardware] = request.URL.Query().Get("state")
		handler.mu.Unlock()

		http.SetCookie(responseWriter, &http.Cookie{Name: "account", Value: hardware, Path: "/"})
		_, _ = responseWriter.Write([]byte(`<input name="csrf-token" value="synthetic">`))

		return
	}

	cookie, err := request.Cookie("account")
	if err != nil {
		handler.t.Error("OAuth cookie missing on completion")

		return
	}

	handler.mu.Lock()
	state := handler.states[cookie.Value]
	handler.mu.Unlock()

	responseWriter.Header().Set("Location", "https://ring.com/signin/callback?code="+cookie.Value+"&state="+state)
	responseWriter.WriteHeader(http.StatusFound)
}

func (handler *pkceReplayHandler) signIn(responseWriter http.ResponseWriter, request *http.Request) {
	cookie, err := request.Cookie("account")
	if err != nil {
		handler.t.Error("OAuth cookie missing on sign-in")

		return
	}

	_ = request.ParseForm()

	valid := false
	for _, account := range handler.accounts {
		valid = valid || (request.Form.Get("username") == account.AccessToken && cookie.Value == account.HardwareID)
	}

	if !valid {
		handler.t.Error("sign-in used another account's cookie")
	}

	responseWriter.WriteHeader(http.StatusPreconditionFailed)
	_, _ = responseWriter.Write([]byte(syntheticEmailTwoFactorState))
}

func (handler *pkceReplayHandler) twoFactor(responseWriter http.ResponseWriter, request *http.Request) {
	_, err := request.Cookie("account")
	if err != nil {
		handler.t.Error("OAuth cookie missing on 2FA")
	}

	_, _ = responseWriter.Write([]byte(`{}`))
}

func (handler *pkceReplayHandler) token(responseWriter http.ResponseWriter, request *http.Request) {
	_ = request.ParseForm()

	responseWriter.Header().Set("Content-Type", "application/json")

	if request.Form.Get("grant_type") == "authorization_code" &&
		request.Form.Get("code") != request.Header.Get("Hardware_id") {
		handler.t.Error("code exchange crossed accounts")
	}

	_, _ = responseWriter.Write(
		[]byte(`{"access_token":"` + request.Header.Get("Hardware_id") +
			`","refresh_token":"synthetic-refresh","token_type":"Bearer"}`),
	)
}
