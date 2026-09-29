package replay

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestLoadExchangePreservesJSONSyntaxCause(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "invalid-exchange.json")

	writeErr := os.WriteFile(path, []byte(`{"request":`), 0o600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}

	_, err := LoadExchange(path)

	var syntaxError *json.SyntaxError

	if !errors.As(err, &syntaxError) {
		t.Fatalf("load error = %v, want a wrapped JSON syntax error", err)
	}
}

func TestTransportCancellationPreservesCause(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	request := httptest.NewRequest(http.MethodGet, "https://example.test/", nil).WithContext(ctx)

	response, err := NewTransport().RoundTrip(request)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("transport error = %v, want context cancellation", err)
	}
}

func TestTransportStrictOnceAndFreshResponses(t *testing.T) {
	t.Parallel()

	exchange := Exchange{
		Request: Request{
			Method:  http.MethodPost,
			Origin:  "https://example.test",
			Path:    "/v1",
			Query:   []Pair{{"x", "1"}, {"x", "2"}},
			Headers: http.Header{"Content-Type": {"application/json"}},
			Body:    []byte(`{"n":9007199254740993,"ok":true}`),
			JSON:    true,
		},
		Response: Response{
			Status:  http.StatusCreated,
			Headers: http.Header{"X-Test": {"yes"}},
			Body:    []byte(`{"result":1}`),
			JSON:    true,
		},
	}
	tr := NewTransport(exchange)
	call := func(body string) *http.Response {
		r := httptest.NewRequest(http.MethodPost, "https://example.test/v1?x=2&x=1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")

		resp, e := tr.RoundTrip(r)
		if e != nil {
			t.Fatal(e)
		}

		return resp
	}
	resp := call(`{ "ok":true,"n":9007199254740993.0 }`)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(b) != `{"result":1}` {
		t.Fatalf("response body: %s", b)
	}

	consumedErr := tr.AssertConsumed()
	if consumedErr != nil {
		t.Fatal(consumedErr)
	}

	assertRejected := func(request *http.Request) {
		t.Helper()

		response, err := tr.RoundTrip(request)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		if err == nil {
			t.Fatal("unexpected request accepted")
		}
	}
	assertRejected(
		httptest.NewRequest(http.MethodPost, "https://example.test/v1?x=1&x=2", strings.NewReader(`{"n":1}`)),
	)
	assertRejected(httptest.NewRequest(http.MethodGet, "https://extra.test/", nil))

	unexpectedRequestErr := tr.AssertConsumed()
	if unexpectedRequestErr == nil {
		t.Fatal("unexpected request was not retained")
	}
}

func TestTransportRequiresRecordedOrder(t *testing.T) {
	t.Parallel()

	first := Exchange{
		Request: Request{
			Method:  http.MethodGet,
			Origin:  "https://example.test",
			Path:    "/first",
			Headers: http.Header{},
		},
		Response: Response{Status: http.StatusOK},
	}
	second := Exchange{
		Request: Request{
			Method:  http.MethodGet,
			Origin:  "https://example.test",
			Path:    "/second",
			Headers: http.Header{},
		},
		Response: Response{Status: http.StatusNoContent},
	}
	request := func(path string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "https://example.test"+path, nil)
	}

	ordered := NewTransport(first, second)

	{
		response, err := ordered.RoundTrip(request("/second"))
		if err == nil || response != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			t.Fatalf("out-of-order request returned response %v, error %v", response, err)
		}
	}

	orderedErr := ordered.AssertConsumed()
	if orderedErr == nil {
		t.Fatal("out-of-order request did not fail consumed assertion")
	}

	independent := NewUnorderedTransport(first, second)
	for _, path := range []string{"/second", "/first"} {
		response, err := independent.RoundTrip(request(path))
		if err != nil {
			t.Fatal(err)
		}

		_ = response.Body.Close()
	}

	independentErr := independent.AssertConsumed()
	if independentErr != nil {
		t.Fatal(independentErr)
	}
}

func TestTransportNilBodyAndCanceledContext(t *testing.T) {
	t.Parallel()

	x := Exchange{
		Request:  Request{Method: http.MethodGet, Origin: "https://example.test", Path: "/", Headers: http.Header{}},
		Response: Response{Status: http.StatusNoContent},
	}
	tr := NewTransport(x)

	response, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://example.test/", nil))
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	response, err = tr.RoundTrip(httptest.NewRequest(http.MethodGet, "https://example.test/", nil).WithContext(ctx))
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err == nil {
		t.Fatal("canceled request accepted")
	}
}

func TestSemanticEqualTypesTrailingValuesAndNull(t *testing.T) {
	t.Parallel()

	if SemanticEqual([]byte(`1`), []byte(`"1"`)) {
		t.Fatal("number equaled string")
	}

	if SemanticEqual([]byte(`{} {}`), []byte(`{}`)) {
		t.Fatal("accepted trailing JSON value")
	}

	if !SemanticEqual([]byte(`1`), []byte(`1.0`)) {
		t.Fatal("equal numeric values did not match")
	}

	if string(decodeBody(json.RawMessage(`null`), true)) != "null" {
		t.Fatal("JSON null lost")
	}
}

func TestCanonicalHeadersMergeCaseVariants(t *testing.T) {
	t.Parallel()

	a := http.Header{"X-Test": {"a"}, "x-test": {"b"}}

	b := http.Header{"X-TEST": {"b", "a"}}

	if !strings.EqualFold(
		strings.Join(canonicalHeaders(a)["x-test"], ","),
		strings.Join(canonicalHeaders(b)["x-test"], ","),
	) {
		t.Fatal("case variant headers not merged")
	}
}

func TestHeadersModeExactDefaultAndRequiredSubset(t *testing.T) {
	t.Parallel()

	want := http.Header{"Accept": {"application/json"}, "X-Key": {"a", "b"}}

	extra := http.Header{"Accept": {"application/json"}, "X-Key": {"b", "a"}, "User-Agent": {"client"}}

	if headersMatch(want, extra, "") || headersMatch(want, extra, HeadersExact) {
		t.Fatal("exact mode accepted extra header")
	}

	if !headersMatch(want, extra, HeadersRequired) {
		t.Fatal("required mode rejected extra headers or reordered values")
	}

	for _, got := range []http.Header{
		{"Accept": {"application/json"}},
		{"Accept": {"application/json"}, "X-Key": {"a"}},
		{"Accept": {"text/plain"}, "X-Key": {"a", "b"}},
	} {
		if headersMatch(want, got, HeadersRequired) {
			t.Fatalf("required mode accepted missing/different/repeated values: %#v", got)
		}
	}

	if headersMatch(want, extra, HeadersMode("typo")) {
		t.Fatal("unknown header mode accepted")
	}
}

func TestTransportRejectsUnexpectedHeaderAndQuery(t *testing.T) {
	t.Parallel()

	exchange := Exchange{
		Request: Request{
			Method:  "GET",
			Origin:  "https://example.test",
			Path:    "/x",
			Query:   []Pair{{"a", "1"}},
			Headers: http.Header{},
		},
		Response: Response{Status: 204},
	}

	tr := NewTransport(exchange)
	for _, target := range []string{"https://example.test/x?a=2", "https://example.test/x?a=1&b=2"} {
		response, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, target, nil))
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}

		if err == nil {
			t.Fatalf("accepted %s", target)
		}
	}

	e := tr.AssertConsumed()
	if e == nil {
		t.Fatal("expected unconsumed assertion")
	}
}

func TestWebSocketScript(t *testing.T) {
	t.Parallel()

	server := NewWebSocketServer(
		[]WSStep{
			{Kind: "expect", Frame: "text", Body: []byte(`{"id":1234567890123456789,"method":"x"}`)},
			{Kind: "send", Frame: "text", Body: []byte(`{"ok":true}`)},
		},
		time.Second,
	)
	defer server.Close()

	connection, response, socketErr := websocket.DefaultDialer.Dial(server.URL(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if socketErr != nil {
		t.Fatal(socketErr)
	}

	defer func() { _ = connection.Close() }()

	{
		socketErr = connection.WriteMessage(websocket.TextMessage, []byte(`{"method":"x","id":1234567890123456789.0}`))
		if socketErr != nil {
			t.Fatal(socketErr)
		}
	}

	_, messageBytes, socketErr := connection.ReadMessage()
	if socketErr != nil {
		t.Fatal(socketErr)
	}

	if string(messageBytes) != `{"ok":true}` {
		t.Fatalf("got %s", messageBytes)
	}

	{
		socketErr = server.AssertComplete(time.Second)
		if socketErr != nil {
			t.Fatal(socketErr)
		}
	}

	{
		socketErr = server.AssertComplete(time.Second)
		if socketErr != nil {
			t.Fatal("completion was not persistent:", socketErr)
		}
	}
}

func TestWebSocketCloseUnblocksActiveScript(t *testing.T) {
	t.Parallel()

	server := NewWebSocketServer([]WSStep{{Kind: "expect", Frame: "text", Body: []byte(`{}`)}}, time.Second)

	connection, response, err := websocket.DefaultDialer.Dial(server.URL(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	server.Close()
	_ = connection.Close()

	{
		err = server.AssertComplete(time.Second)
		if err == nil {
			t.Fatal("expected active script to report disconnect")
		}
	}
}

func TestWebSocketReplayRejectsUpgradeAndTrailingFrame(t *testing.T) {
	t.Parallel()

	badUpgrade := NewWebSocketServer(
		nil,
		time.Second,
		WSHandshake{Path: "/", Query: map[string][]string{"token": {"expected"}}},
	)

	_, response, err := websocket.DefaultDialer.Dial(badUpgrade.URL()+"?token=wrong", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err == nil {
		t.Fatal("wrong upgrade query was accepted")
	}

	{
		err := badUpgrade.AssertComplete(time.Second)
		if err == nil {
			t.Fatal("wrong upgrade did not fail replay assertion")
		}
	}

	badUpgrade.Close()

	for _, tc := range []struct {
		name    string
		want    WSHandshake
		headers http.Header
	}{
		{
			name: "origin",
			want: WSHandshake{Path: "/", Origin: "https://expected.example"},
			headers: http.Header{
				"Origin": {"https://unexpected.example"},
			},
		},
		{name: "host", want: WSHandshake{Path: "/", Host: "expected.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := NewWebSocketServer(nil, time.Second, tc.want)
			t.Cleanup(server.Close)

			_, response, dialErr := websocket.DefaultDialer.Dial(server.URL(), tc.headers)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}

			if dialErr == nil || server.AssertComplete(time.Second) == nil {
				t.Fatal("mismatched upgrade origin or host was accepted")
			}
		})
	}

	extra := NewWebSocketServer([]WSStep{{Kind: "expect", Frame: "text", Body: []byte(`{"step":1}`)}}, time.Second)
	t.Cleanup(extra.Close)

	conn, response, err := websocket.DefaultDialer.Dial(extra.URL(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	{
		err := conn.WriteMessage(websocket.TextMessage, []byte(`{"step":1}`))
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := conn.WriteMessage(websocket.TextMessage, []byte(`{"extra":true}`))
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := extra.AssertComplete(time.Second)
		if err == nil {
			t.Fatal("trailing frame did not fail replay assertion")
		}
	}
}
