package replay

import (
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
	if _, e := tr.RoundTrip(httptest.NewRequest("POST", "https://example.test/v1?x=1&x=2", strings.NewReader(`{"n":1}`))); e == nil {
		t.Fatal("mismatched/consumed exchange unexpectedly replayed")
	}
	if e := tr.AssertConsumed(); e != nil {
		t.Fatal(e)
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
	defer w.Server.Close()
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
}
