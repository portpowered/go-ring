package replay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestTransportStrictOnceAndFreshResponses(t *testing.T) {
	x := Exchange{Request: Request{Method: http.MethodPost, Origin: "https://example.test", Path: "/v1", Query: []Pair{{"x", "1"}, {"x", "2"}}, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"n":9007199254740993,"ok":true}`), JSON: true}, Response: Response{Status: http.StatusCreated, Headers: http.Header{"X-Test": {"yes"}}, Body: []byte(`{"result":1}`), JSON: true}}
	tr := NewTransport(x)
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
	if e := tr.AssertConsumed(); e != nil {
		t.Fatal(e)
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
	assertRejected(httptest.NewRequest(http.MethodPost, "https://example.test/v1?x=1&x=2", strings.NewReader(`{"n":1}`)))
	assertRejected(httptest.NewRequest(http.MethodGet, "https://extra.test/", nil))
	if e := tr.AssertConsumed(); e == nil {
		t.Fatal("unexpected request was not retained")
	}
}

func TestTransportRequiresRecordedOrder(t *testing.T) {
	first := Exchange{Request: Request{Method: http.MethodGet, Origin: "https://example.test", Path: "/first", Headers: http.Header{}}, Response: Response{Status: http.StatusOK}}
	second := Exchange{Request: Request{Method: http.MethodGet, Origin: "https://example.test", Path: "/second", Headers: http.Header{}}, Response: Response{Status: http.StatusNoContent}}
	request := func(path string) *http.Request {
		return httptest.NewRequest(http.MethodGet, "https://example.test"+path, nil)
	}
	ordered := NewTransport(first, second)
	if response, err := ordered.RoundTrip(request("/second")); err == nil || response != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		t.Fatalf("out-of-order request returned response %v, error %v", response, err)
	}
	if err := ordered.AssertConsumed(); err == nil {
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
	if err := independent.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestTransportNilBodyAndCanceledContext(t *testing.T) {
	x := Exchange{Request: Request{Method: http.MethodGet, Origin: "https://example.test", Path: "/", Headers: http.Header{}}, Response: Response{Status: http.StatusNoContent}}
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
	a := http.Header{"X-Test": {"a"}, "x-test": {"b"}}
	b := http.Header{"X-TEST": {"b", "a"}}
	if !strings.EqualFold(strings.Join(canonicalHeaders(a)["x-test"], ","), strings.Join(canonicalHeaders(b)["x-test"], ",")) {
		t.Fatal("case variant headers not merged")
	}
}

func TestHeadersModeExactDefaultAndRequiredSubset(t *testing.T) {
	want := http.Header{"Accept": {"application/json"}, "X-Key": {"a", "b"}}
	extra := http.Header{"Accept": {"application/json"}, "X-Key": {"b", "a"}, "User-Agent": {"client"}}
	if headersMatch(want, extra, "") || headersMatch(want, extra, HeadersExact) {
		t.Fatal("exact mode accepted extra header")
	}
	if !headersMatch(want, extra, HeadersRequired) {
		t.Fatal("required mode rejected extra headers or reordered values")
	}
	for _, got := range []http.Header{{"Accept": {"application/json"}}, {"Accept": {"application/json"}, "X-Key": {"a"}}, {"Accept": {"text/plain"}, "X-Key": {"a", "b"}}} {
		if headersMatch(want, got, HeadersRequired) {
			t.Fatalf("required mode accepted missing/different/repeated values: %#v", got)
		}
	}
	if headersMatch(want, extra, HeadersMode("typo")) {
		t.Fatal("unknown header mode accepted")
	}
}

func TestTransportRejectsUnexpectedHeaderAndQuery(t *testing.T) {
	x := Exchange{Request: Request{Method: "GET", Origin: "https://example.test", Path: "/x", Query: []Pair{{"a", "1"}}, Headers: http.Header{}}, Response: Response{Status: 204}}
	tr := NewTransport(x)
	for _, target := range []string{"https://example.test/x?a=2", "https://example.test/x?a=1&b=2"} {
		response, err := tr.RoundTrip(httptest.NewRequest(http.MethodGet, target, nil))
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err == nil {
			t.Fatalf("accepted %s", target)
		}
	}
	if e := tr.AssertConsumed(); e == nil {
		t.Fatal("expected unconsumed assertion")
	}
}

func TestWebSocketScript(t *testing.T) {
	w := NewWebSocketServer([]WSStep{{Kind: "expect", Frame: "text", Body: []byte(`{"id":1234567890123456789,"method":"x"}`)}, {Kind: "send", Frame: "text", Body: []byte(`{"ok":true}`)}}, time.Second)
	defer w.Close()
	c, response, e := websocket.DefaultDialer.Dial(w.URL(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = c.Close() }()
	if e = c.WriteMessage(websocket.TextMessage, []byte(`{"method":"x","id":1234567890123456789.0}`)); e != nil {
		t.Fatal(e)
	}
	_, b, e := c.ReadMessage()
	if e != nil {
		t.Fatal(e)
	}
	if string(b) != `{"ok":true}` {
		t.Fatalf("got %s", b)
	}
	if e = w.AssertComplete(time.Second); e != nil {
		t.Fatal(e)
	}
	if e = w.AssertComplete(time.Second); e != nil {
		t.Fatal("completion was not persistent:", e)
	}
}

func TestWebSocketCloseUnblocksActiveScript(t *testing.T) {
	w := NewWebSocketServer([]WSStep{{Kind: "expect", Frame: "text", Body: []byte(`{}`)}}, time.Second)
	c, response, err := websocket.DefaultDialer.Dial(w.URL(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	_ = c.Close()
	if err = w.AssertComplete(time.Second); err == nil {
		t.Fatal("expected active script to report disconnect")
	}
}

func TestWebSocketReplayRejectsUpgradeAndTrailingFrame(t *testing.T) {
	badUpgrade := NewWebSocketServer(nil, time.Second, WSHandshake{Path: "/", Query: map[string][]string{"token": {"expected"}}})
	_, response, err := websocket.DefaultDialer.Dial(badUpgrade.URL()+"?token=wrong", nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("wrong upgrade query was accepted")
	}
	if err := badUpgrade.AssertComplete(time.Second); err == nil {
		t.Fatal("wrong upgrade did not fail replay assertion")
	}
	badUpgrade.Close()

	for _, tc := range []struct {
		name    string
		want    WSHandshake
		headers http.Header
	}{
		{name: "origin", want: WSHandshake{Path: "/", Origin: "https://expected.example"}, headers: http.Header{"Origin": {"https://unexpected.example"}}},
		{name: "host", want: WSHandshake{Path: "/", Host: "expected.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := NewWebSocketServer(nil, time.Second, tc.want)
			defer server.Close()
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
	defer extra.Close()
	conn, response, err := websocket.DefaultDialer.Dial(extra.URL(), nil)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"step":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"extra":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := extra.AssertComplete(time.Second); err == nil {
		t.Fatal("trailing frame did not fail replay assertion")
	}
}
