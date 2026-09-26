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
	data, err := os.ReadFile("fixtures/porting/account-scope.json")
	require.NoError(t, err)
	var fixture struct {
		Accounts []replayAccount `json:"accounts"`
	}
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.Len(t, fixture.Accounts, 2)
	return fixture.Accounts
}

func TestAccountRequestScopeReplay(t *testing.T) {
	accounts := loadReplayAccounts(t)
	wantHardware := map[string]string{}
	for _, account := range accounts {
		wantHardware[account.AccessToken] = account.HardwareID
	}
	var mu sync.Mutex
	seen := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		hardware := r.Header.Get("hardware_id")
		if wantHardware[token] != hardware || token == "" {
			t.Errorf("cross-account headers: token=%q hardware=%q", token, hardware)
		}
		if r.URL.Path == "/clients_api/session" {
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), hardware) {
				t.Errorf("registration body omits request hardware ID")
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path != "/device_info/v3/devices" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		seen[token]++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"devices":[]}`))
	}))
	defer server.Close()
	client, err := ring.NewClient(ring.WithHTTPClient(server.Client()), ring.WithEndpoints(ring.Endpoints{APIBaseURL: server.URL}))
	require.NoError(t, err)
	defer client.Close()
	var group sync.WaitGroup
	for _, account := range accounts {
		group.Add(1)
		go func(account replayAccount) {
			defer group.Done()
			for range 10 {
				_, callErr := client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: ring.AccountAuth{AccessToken: account.AccessToken, HardwareID: account.HardwareID}})
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
	_, err = client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: ring.AccountAuth{HardwareID: "missing-token"}})
	require.True(t, ringapimodels.IsTokenError(err), "missing request token: %v", err)
}

func TestLoginSessionsDoNotAuthorizeSharedClient(t *testing.T) {
	accounts := loadReplayAccounts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r) // select the recorded legacy fallback
			return
		}
		_ = r.ParseForm()
		for _, account := range accounts {
			if r.Form.Get("username") == account.AccessToken {
				if r.Header.Get("hardware_id") != account.HardwareID {
					t.Errorf("login hardware mismatch")
				}
				_, _ = w.Write([]byte(`{"access_token":"` + account.AccessToken + `","refresh_token":"refresh","token_type":"Bearer"}`))
				return
			}
		}
		http.Error(w, "wrong account", http.StatusBadRequest)
	}))
	defer server.Close()
	client, err := ring.NewClient(ring.WithHTTPClient(server.Client()), ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: server.URL}))
	require.NoError(t, err)
	defer client.Close()
	var group sync.WaitGroup
	for _, account := range accounts {
		group.Add(1)
		go func(account replayAccount) {
			defer group.Done()
			flow, flowErr := client.NewLoginSession(ring.LoginSessionRequest{Username: account.AccessToken, Password: "synthetic-password", HardwareID: account.HardwareID})
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
			if closeErr := flow.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}(account)
	}
	group.Wait()
	_, err = client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
	require.True(t, ringapimodels.IsTokenError(err), "login leaked authorization onto shared client: %v", err)
}

func TestSignalingConnectionsBindRequestAccount(t *testing.T) {
	accounts := loadReplayAccounts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path == "/clients_api/session" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/api/v1/clap/ticket/request/signalsocket" {
			if token == "" {
				t.Error("ticket request has no account authorization")
			}
			_, _ = w.Write([]byte(`{"ticket":"` + token + `"}`))
			return
		}
		if r.URL.Path == "/ws" {
			valid := false
			for _, account := range accounts {
				valid = valid || (r.URL.Query().Get("ticket") == account.AccessToken && r.Header.Get("hardware_id") == account.HardwareID)
			}
			if !valid {
				t.Error("signaling ticket and hardware identity crossed accounts")
			}
			conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
			if err == nil {
				defer conn.Close()
				for {
					if _, _, err = conn.ReadMessage(); err != nil {
						return
					}
				}
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client, err := ring.NewClient(
		ring.WithHTTPClient(server.Client()),
		ring.WithEndpoints(ring.Endpoints{APIBaseURL: server.URL, SolutionsBaseURL: server.URL}),
		ring.WithSignalingWebSocketURL("ws"+strings.TrimPrefix(server.URL, "http")+"/ws?ticket={token}"),
	)
	require.NoError(t, err)
	defer client.Close()
	for _, account := range accounts {
		conn, openErr := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{Auth: ring.AccountAuth{AccessToken: account.AccessToken, HardwareID: account.HardwareID}})
		require.NoError(t, openErr)
		require.NoError(t, conn.Close())
	}
}

func TestEventConnectionsUseRequestAccount(t *testing.T) {
	accounts := loadReplayAccounts(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		valid := false
		for _, account := range accounts {
			valid = valid || (r.Header.Get("Authorization") == "Bearer "+account.AccessToken && r.Header.Get("hardware_id") == account.HardwareID)
		}
		if !valid {
			t.Error("event socket received a different account's credentials")
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]any{"kind": "ding", "device_id": 123})
	}))
	defer server.Close()
	client, err := ring.NewClient(ring.WithEventWebSocketURL("ws" + strings.TrimPrefix(server.URL, "http")))
	require.NoError(t, err)
	defer client.Close()
	for _, account := range accounts {
		conn, connectErr := client.ConnectEvents(context.Background(), ring.ConnectEventsRequest{Auth: ring.AccountAuth{AccessToken: account.AccessToken, HardwareID: account.HardwareID}})
		require.NoError(t, connectErr)
		event, receiveErr := conn.Receive()
		require.NoError(t, receiveErr)
		require.Equal(t, int64(123), event.DeviceID)
		require.NoError(t, conn.Close())
	}
}

func TestGeneratedTicketRequestUsesAccountScope(t *testing.T) {
	account := loadReplayAccounts(t)[0]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+account.AccessToken || r.Header.Get("hardware_id") != account.HardwareID {
			t.Error("generated request did not receive scoped authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ticket":"synthetic-ticket"}`))
	}))
	defer server.Close()
	client, err := ring.NewClient(ring.WithHTTPClient(server.Client()), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: server.URL}))
	require.NoError(t, err)
	defer client.Close()
	ticket, err := client.GetCapturedTickets(context.Background(), ring.GetCapturedTicketsRequest{Auth: ring.AccountAuth{AccessToken: account.AccessToken, HardwareID: account.HardwareID}})
	require.NoError(t, err)
	require.Equal(t, "synthetic-ticket", ticket.Ticket)
}

func TestConcurrentPKCELoginSessionsKeepChallengesSeparate(t *testing.T) {
	accounts := loadReplayAccounts(t)
	var mu sync.Mutex
	states := map[string]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/v2/authorize":
			if r.URL.Query().Get("response_type") == "code" {
				hardware := r.URL.Query().Get("hardware_id")
				mu.Lock()
				states[hardware] = r.URL.Query().Get("state")
				mu.Unlock()
				http.SetCookie(w, &http.Cookie{Name: "account", Value: hardware, Path: "/"})
				_, _ = w.Write([]byte(`<input name="csrf-token" value="synthetic">`))
				return
			}
			cookie, err := r.Cookie("account")
			if err != nil {
				t.Error("OAuth cookie missing on completion")
				return
			}
			mu.Lock()
			state := states[cookie.Value]
			mu.Unlock()
			w.Header().Set("Location", "https://ring.com/signin/callback?code="+cookie.Value+"&state="+state)
			w.WriteHeader(http.StatusFound)
		case "/oauth/v2/signin":
			cookie, err := r.Cookie("account")
			if err != nil {
				t.Error("OAuth cookie missing on sign-in")
				return
			}
			_ = r.ParseForm()
			valid := false
			for _, account := range accounts {
				valid = valid || (r.Form.Get("username") == account.AccessToken && cookie.Value == account.HardwareID)
			}
			if !valid {
				t.Error("sign-in used another account's cookie")
			}
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"tsv_state":"email"}`))
		case "/oauth/v2/2fa/verify":
			if _, err := r.Cookie("account"); err != nil {
				t.Error("OAuth cookie missing on 2FA")
			}
			_, _ = w.Write([]byte(`{}`))
		case "/oauth/token":
			_ = r.ParseForm()
			w.Header().Set("Content-Type", "application/json")
			if r.Form.Get("grant_type") == "authorization_code" {
				if r.Form.Get("code") != r.Header.Get("hardware_id") {
					t.Error("code exchange crossed accounts")
				}
			}
			_, _ = w.Write([]byte(`{"access_token":"` + r.Header.Get("hardware_id") + `","refresh_token":"synthetic-refresh","token_type":"Bearer"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := ring.NewClient(ring.WithHTTPClient(server.Client()), ring.WithEndpoints(ring.Endpoints{OAuthBaseURL: server.URL}))
	require.NoError(t, err)
	defer client.Close()
	var group sync.WaitGroup
	for _, account := range accounts {
		group.Add(1)
		go func(account replayAccount) {
			defer group.Done()
			flow, flowErr := client.NewLoginSession(ring.LoginSessionRequest{Username: account.AccessToken, Password: "synthetic", HardwareID: account.HardwareID})
			if flowErr != nil {
				t.Error(flowErr)
				return
			}
			defer flow.Close()
			if err := flow.Request2FACode(context.Background()); err != nil {
				t.Error(err)
				return
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
