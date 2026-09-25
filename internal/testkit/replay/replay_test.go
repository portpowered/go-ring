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
	x := Exchange{Request: Request{Method: "POST", Origin: "https://example.test", Path: "/v1", Query: []Pair{{"x", "1"}, {"x", "2"}}, Headers: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"n":9007199254740993,"ok":true}`), JSON: true}, Response: Response{Status: 201, Headers: http.Header{"X-Test": {"yes"}}, Body: []byte(`{"result":1}`), JSON: true}}
	tr := NewTransport(x)
	call := func(body string) *http.Response {
		r := httptest.NewRequest("POST", "https://example.test/v1?x=2&x=1", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		resp, e := tr.RoundTrip(r)
		if e != nil {
			t.Fatal(e)
		}
		return resp
	}
	resp := call(`{ "ok":true,"n":9007199254740993.0 }`)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != `{"result":1}` {
		t.Fatalf("response body: %s", b)
	}
	if e := tr.AssertConsumed(); e != nil {
		t.Fatal(e)
	}
	if _, e := tr.RoundTrip(httptest.NewRequest("POST", "https://example.test/v1?x=1&x=2", strings.NewReader(`{"n":1}`))); e == nil {
		t.Fatal("mismatched/consumed exchange unexpectedly replayed")
	}
	if _, e := tr.RoundTrip(httptest.NewRequest("GET", "https://extra.test/", nil)); e == nil {
		t.Fatal("unexpected request accepted")
	}
	if e := tr.AssertConsumed(); e == nil {
		t.Fatal("unexpected request was not retained")
	}
}

func TestTransportNilBodyAndCanceledContext(t *testing.T) {
	x := Exchange{Request: Request{Method: "GET", Origin: "https://example.test", Path: "/", Headers: http.Header{}}, Response: Response{Status: 204}}
	tr := NewTransport(x)
	if _, err := tr.RoundTrip(httptest.NewRequest("GET", "https://example.test/", nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tr.RoundTrip(httptest.NewRequest("GET", "https://example.test/", nil).WithContext(ctx)); err == nil {
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

func TestTransportRejectsUnexpectedHeaderAndQuery(t *testing.T) {
	x := Exchange{Request: Request{Method: "GET", Origin: "https://example.test", Path: "/x", Query: []Pair{{"a", "1"}}, Headers: http.Header{}}, Response: Response{Status: 204}}
	tr := NewTransport(x)
	for _, target := range []string{"https://example.test/x?a=2", "https://example.test/x?a=1&b=2"} {
		if _, e := tr.RoundTrip(httptest.NewRequest("GET", target, nil)); e == nil {
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
	c, _, e := websocket.DefaultDialer.Dial(w.URL(), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
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
	c, _, err := websocket.DefaultDialer.Dial(w.URL(), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	_ = c.Close()
	if err = w.AssertComplete(time.Second); err == nil {
		t.Fatal("expected active script to report disconnect")
	}
}
