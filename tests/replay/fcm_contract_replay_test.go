package replay_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/dependencies/push"
	"github.com/stretchr/testify/require"
)

type fcmReplayMutation struct {
	name    string
	fixture string
	mutate  func(*testing.T, *http.Request)
}

func TestFCMHTTPDiagnosticRoundTripsPairedRegistration(t *testing.T) {
	t.Parallel()

	exchange := loadFCMExchange(t, "registration")
	transport := replay.NewTransport(exchange)
	request := fcmRequestFromExchange(t, exchange)

	response, err := push.NewRegistrationTransport(transport).RoundTrip(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "application/json", response.Header.Get("Content-Type"))
	require.NoError(t, response.Body.Close())
	require.NoError(t, transport.AssertConsumed())
}

func TestFCMHTTPDiagnosticRejectsRouteAndHeaderMutations(t *testing.T) {
	t.Parallel()

	runFCMReplayMutations(t, fcmRouteAndHeaderMutations())
}

func TestFCMHTTPDiagnosticRejectsBodyMutations(t *testing.T) {
	t.Parallel()

	runFCMReplayMutations(t, fcmBodyMutations())
}

func fcmRouteAndHeaderMutations() []fcmReplayMutation {
	return []fcmReplayMutation{
		{
			name:    "query added to inventoried route",
			fixture: "registration",
			mutate: func(_ *testing.T, request *http.Request) {
				request.URL.RawQuery = "unexpected=1"
			},
		},
		{
			name:    "host header changes authority",
			fixture: "registration",
			mutate: func(_ *testing.T, request *http.Request) {
				request.Host = "attacker.example"
			},
		},
		{
			name:    "wrong method",
			fixture: "registration",
			mutate: func(_ *testing.T, request *http.Request) {
				request.Method = http.MethodGet
			},
		},
		{
			name:    "extra registration header",
			fixture: "registration",
			mutate: func(_ *testing.T, request *http.Request) {
				request.Header.Set("X-Unlisted", "value")
			},
		},
	}
}

func fcmBodyMutations() []fcmReplayMutation {
	return []fcmReplayMutation{
		{
			name:    "malformed registration JSON",
			fixture: "registration",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				setFCMRequestBody(t, request, []byte("{"))
			},
		},
		{
			name:    "changed legacy VAPID value",
			fixture: "registration",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				mutateFCMWebField(t, request, "applicationPubKey", json.RawMessage(`"wrong-key"`))
			},
		},
		{
			name:    "registration endpoint outside pinned prefix",
			fixture: "registration",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				mutateFCMWebField(t, request, "endpoint", json.RawMessage(`"https://attacker.example/send"`))
			},
		},
		{
			name:    "malformed public key",
			fixture: "registration",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				mutateFCMWebField(t, request, "p256dh", json.RawMessage(`"not-base64"`))
			},
		},
		{
			name:    "malformed registration web object",
			fixture: "registration",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				mutateFCMWebField(t, request, "web", json.RawMessage(`"not-an-object"`))
			},
		},
		{
			name:    "malformed check-in protobuf",
			fixture: "checkin",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				setFCMRequestBody(t, request, []byte{0xff})
			},
		},
		{
			name:    "legacy authorization is not decimal",
			fixture: "legacy-registration",
			mutate: func(_ *testing.T, request *http.Request) {
				request.Header.Set("Authorization", "AidLogin abc:67890")
			},
		},
		{
			name:    "malformed legacy form escape",
			fixture: "legacy-registration",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				setFCMRequestBody(t, request, []byte("app=%zz"))
			},
		},
		{
			name:    "installation has an unlisted property",
			fixture: "installation",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				setFCMRequestBody(t, request, []byte(`{"unexpected":true}`))
			},
		},
		{
			name:    "installation body is not an object",
			fixture: "installation",
			mutate: func(t *testing.T, request *http.Request) {
				t.Helper()

				setFCMRequestBody(t, request, []byte("[]"))
			},
		},
	}
}

func runFCMReplayMutations(t *testing.T, tests []fcmReplayMutation) {
	t.Helper()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			exchange := loadFCMExchange(t, test.fixture)
			transport := replay.NewTransport(exchange)
			request := fcmRequestFromExchange(t, exchange)
			test.mutate(t, request)

			response, err := push.NewRegistrationTransport(transport).RoundTrip(request)

			if response != nil {
				require.NoError(t, response.Body.Close())
			}

			require.Error(t, err)
			require.True(t, ringerrors.IsNetworkError(err) || ringerrors.IsBadRequestError(err))
			require.NotContains(t, err.Error(), "replay: no unused exchange matches")
			require.Error(t, transport.AssertConsumed(), "tampered request must not consume the paired response")
		})
	}
}

func loadFCMExchange(t *testing.T, name string) replay.Exchange {
	t.Helper()

	exchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "fcm", name+".json"))
	require.NoError(t, err)

	return exchange
}

func fcmRequestFromExchange(t *testing.T, exchange replay.Exchange) *http.Request {
	t.Helper()

	request := requestFromExchange(t, exchange)
	if exchange.Request.BodyEncoding != "" || len(exchange.Request.Body) == 0 || string(exchange.Request.Body) == "null" {
		return request
	}

	body := exchange.Request.Body

	if !exchange.Request.JSON {
		var text string

		require.NoError(t, json.Unmarshal(body, &text))

		body = []byte(text)
	}

	setFCMRequestBody(t, request, body)

	return request
}

func readFCMRequestBody(t *testing.T, request *http.Request) []byte {
	t.Helper()

	body, err := io.ReadAll(request.Body)
	require.NoError(t, err)
	setFCMRequestBody(t, request, body)

	return body
}

func setFCMRequestBody(t *testing.T, request *http.Request, body []byte) {
	t.Helper()

	if request.Body != nil {
		require.NoError(t, request.Body.Close())
	}

	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
}

func mutateFCMWebField(t *testing.T, request *http.Request, name string, value json.RawMessage) {
	t.Helper()

	body := readFCMRequestBody(t, request)

	var fields map[string]json.RawMessage

	require.NoError(t, json.Unmarshal(body, &fields))

	if name == "web" {
		fields[name] = value
	} else {
		var web map[string]json.RawMessage

		require.NoError(t, json.Unmarshal(fields["web"], &web))

		web[name] = value
		encodedWeb, err := json.Marshal(web)

		require.NoError(t, err)

		fields["web"] = encodedWeb
	}

	encoded, err := json.Marshal(fields)

	require.NoError(t, err)

	setFCMRequestBody(t, request, encoded)
}

func TestFCMHTTPDiagnosticClassifiesPairedServerFailure(t *testing.T) {
	t.Parallel()

	exchange := loadFCMExchange(t, "registration")
	exchange.Response.Status = http.StatusServiceUnavailable
	exchange.Response.JSON = true
	exchange.Response.Body = json.RawMessage(`{"error":"synthetic unavailable"}`)
	transport := replay.NewTransport(exchange)
	request := fcmRequestFromExchange(t, exchange)

	response, err := push.NewRegistrationTransport(transport).RoundTrip(request)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.NoError(t, response.Body.Close())
	require.NoError(t, transport.AssertConsumed())
}
