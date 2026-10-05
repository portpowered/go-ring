package replay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestFrameTemplateBindsOnlyUUIDAndExactReferences(t *testing.T) {
	t.Parallel()

	const (
		first = `{"dialog_id":"$uuid:dialog","body":{"value":1}}`
		good  = `{"dialog_id":"30f4af1f-b705-42e7-ab2d-baf8bbca9657","body":{"value":1}}`
	)

	for name, actual := range map[string]string{
		"non UUID":      `{"dialog_id":"anything","body":{"value":1}}`,
		"extra key":     `{"dialog_id":"30f4af1f-b705-42e7-ab2d-baf8bbca9657","body":{"value":1,"extra":true}}`,
		"changed value": `{"dialog_id":"30f4af1f-b705-42e7-ab2d-baf8bbca9657","body":{"value":2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := matchFrameTemplate([]byte(first), []byte(actual), map[string]string{})
			if err == nil {
				t.Fatal("template accepted invalid frame")
			}
		})
	}

	bindings := map[string]string{}
	{
		err := matchFrameTemplate([]byte(first), []byte(good), bindings)
		if err != nil {
			t.Fatal(err)
		}
	}

	{
		err := matchFrameTemplate(
			[]byte(`{"dialog_id":"$ref:dialog"}`),
			[]byte(`{"dialog_id":"different"}`),
			bindings,
		)
		if err == nil {
			t.Fatal("template accepted a changed binding")
		}
	}

	server, err := renderFrameTemplate([]byte(`{"dialog_id":"$ref:dialog"}`), bindings)
	if err != nil || !strings.Contains(string(server), bindings["dialog"]) {
		t.Fatalf("rendered response = %s, %v", server, err)
	}

	{
		_, err := renderFrameTemplate([]byte(`{"dialog_id":"$ref:missing"}`), bindings)
		if err == nil {
			t.Fatal("template rendered an unbound reference")
		}
	}
}

func TestFrameTemplateBindsBoundedSDPAndCorrelatedRPCIDs(t *testing.T) {
	t.Parallel()

	const offer = "v=0\r\n" +
		"o=- 1 1 IN IP4 127.0.0.1\r\n" +
		"s=-\r\n" +
		"t=0 0\r\n" +
		"m=video 9 UDP/TLS/RTP/SAVPF 96\r\n" +
		"c=IN IP4 0.0.0.0\r\n" +
		"a=ice-ufrag:synthetic\r\n" +
		"a=ice-pwd:synthetic-password\r\n" +
		"a=fingerprint:sha-256 00:11\r\n" +
		"a=setup:actpass\r\n"

	bindings := map[string]string{}

	err := matchFrameTemplate(
		[]byte(`{"body":{"sdp":"$sdp:offer"}}`),
		[]byte(fmt.Sprintf(`{"body":{"sdp":%q}}`, offer)),
		bindings,
	)
	if err != nil || bindings["offer"] != offer {
		t.Fatalf("valid SDP binding = %q, %v", bindings["offer"], err)
	}

	for name, invalid := range map[string]string{
		"oversized":   offer + strings.Repeat("a", maxSDPTemplateBytes),
		"missing ICE": "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\n",
		"bare LF":     strings.ReplaceAll(offer, "\r\n", "\n"),
		"bare CR":     strings.ReplaceAll(offer, "\r\n", "\r"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actual, marshalErr := json.Marshal(map[string]any{"body": map[string]any{"sdp": invalid}})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}

			matchErr := matchFrameTemplate([]byte(`{"body":{"sdp":"$sdp:offer"}}`), actual, map[string]string{})
			if matchErr == nil {
				t.Fatal("invalid SDP binding was accepted")
			}
		})
	}

	for _, actual := range []struct {
		name string
		id   string
	}{
		{name: "wrong prefix", id: "other-control-1"},
		{name: "wrong sequence", id: "control-view-2"},
		{name: "missing sequence", id: "control-view"},
	} {
		t.Run(actual.name, func(t *testing.T) {
			t.Parallel()

			body, marshalErr := json.Marshal(map[string]any{"id": actual.id})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}

			matchErr := matchFrameTemplate([]byte(`{"id":"$rpcid:control-view:1:arrow"}`), body, map[string]string{})
			if matchErr == nil {
				t.Fatal("invalid RPC ID was accepted")
			}
		})
	}

	err = matchFrameTemplate(
		[]byte(`{"id":"$rpcid:control-view:1:arrow"}`),
		[]byte(`{"id":"control-view-1"}`),
		bindings,
	)
	if err != nil || bindings["arrow"] != "control-view-1" {
		t.Fatalf("valid RPC ID binding = %q, %v", bindings["arrow"], err)
	}

	reply, err := renderFrameTemplate([]byte(`{"id":"$ref:arrow"}`), bindings)
	if err != nil || !strings.Contains(string(reply), `"id":"control-view-1"`) {
		t.Fatalf("rendered correlated RPC response = %s, %v", reply, err)
	}
}

func TestWebSocketExactHandshakeAndRequiredClose(t *testing.T) {
	t.Parallel()

	wantHeaders := http.Header{"User-Agent": {"ring-replay-test"}, "Hardware_id": {"hardware-1"}}

	peer := NewWebSocketServer(
		[]WSStep{{Kind: "expect", Frame: "text", Body: []byte(`{"close":true}`)}},
		time.Second,
		WSHandshake{Path: "/", Query: map[string][]string{}, Headers: wantHeaders, ExactHeaders: true, RequireClose: true},
	)

	defer peer.Close()

	clientHeaders := wantHeaders.Clone()

	connection, response, err := websocket.DefaultDialer.Dial(peer.URL(), clientHeaders)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatalf("dial strict replay peer: %v (peer: %v)", err, peer.AssertComplete(time.Second))
	}

	err = connection.WriteMessage(websocket.TextMessage, []byte(`{"close":true}`))
	if err != nil {
		t.Fatal(err)
	}

	err = connection.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	_ = connection.Close()

	err = peer.AssertComplete(time.Second)
	if err != nil {
		t.Fatal(err)
	}

	noClose := NewWebSocketServer(nil, 30*time.Millisecond, WSHandshake{
		Path: "/", Headers: http.Header{"User-Agent": {"ring-replay-test"}}, ExactHeaders: true, RequireClose: true,
	})

	t.Cleanup(noClose.Close)

	clientHeaders = http.Header{"User-Agent": {"ring-replay-test"}}

	connection, response, err = websocket.DefaultDialer.Dial(noClose.URL(), clientHeaders)
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = connection.Close() }()

	err = noClose.AssertComplete(time.Second)
	if err == nil {
		t.Fatal("required WebSocket close was replaced by a terminal read timeout")
	}

	extraHeader := NewWebSocketServer(nil, time.Second, WSHandshake{
		Path: "/", Headers: http.Header{"User-Agent": {"ring-replay-test"}}, ExactHeaders: true,
	})

	t.Cleanup(extraHeader.Close)

	_, response, err = websocket.DefaultDialer.Dial(extraHeader.URL(), http.Header{
		"User-Agent":   {"ring-replay-test"},
		"X-Unexpected": {"secret"},
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err == nil {
		t.Fatal("strict handshake accepted an unlisted application header")
	}

	peerErr := extraHeader.AssertComplete(time.Second)
	if peerErr == nil {
		t.Fatal("strict replay peer did not reject the unexpected header")
	}
}

func TestWebSocketStrictOriginMatchesExactlyOneValue(t *testing.T) {
	t.Parallel()

	const expectedOrigin = "https://synthetic.example"

	applicationHeaders := http.Header{"User-Agent": {"ring-replay-test"}}

	valid := NewWebSocketServer(nil, time.Second, WSHandshake{
		Origin: expectedOrigin, Path: "/", Headers: applicationHeaders, ExactHeaders: true,
	})

	t.Cleanup(valid.Close)

	conn, response, err := websocket.DefaultDialer.Dial(valid.URL(), http.Header{
		"User-Agent": {"ring-replay-test"},
		"Origin":     {expectedOrigin},
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatalf("strict expected Origin was rejected: %v", err)
	}

	_ = conn.Close()

	err = valid.AssertComplete(time.Second)
	if err != nil {
		t.Fatal(err)
	}

	duplicate := NewWebSocketServer(nil, time.Second, WSHandshake{
		Origin: expectedOrigin, Path: "/", Headers: applicationHeaders, ExactHeaders: true,
	})

	t.Cleanup(duplicate.Close)

	_, response, err = websocket.DefaultDialer.Dial(duplicate.URL(), http.Header{
		"User-Agent": {"ring-replay-test"},
		"Origin":     {expectedOrigin, "https://unexpected.example"},
	})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err == nil {
		t.Fatal("strict handshake accepted a matching first Origin followed by another value")
	}

	peerErr := duplicate.AssertComplete(time.Second)
	if peerErr == nil {
		t.Fatal("strict replay peer did not reject duplicate Origin values")
	}
}

func TestTemplateMismatchNeverSendsStoredResponse(t *testing.T) {
	t.Parallel()

	peer := NewWebSocketServer([]WSStep{
		{
			Kind:     "expect",
			Frame:    "text",
			Template: true,
			Body:     []byte(`{"dialog_id":"$uuid:dialog","body":{"value":1}}`),
		},
		{Kind: "send", Frame: "text", Template: true, Body: []byte(`{"dialog_id":"$ref:dialog","ok":true}`)},
	}, time.Second)
	defer peer.Close()

	conn, response, err := websocket.DefaultDialer.Dial(peer.URL(), http.Header{})
	if response != nil && response.Body != nil {
		_ = response.Body.Close()
	}

	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	{
		err := conn.WriteMessage(websocket.TextMessage, []byte(`{"dialog_id":"wrong","body":{"value":1}}`))
		if err != nil {
			t.Fatal(err)
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	{
		_, _, err := conn.ReadMessage()
		if err == nil {
			t.Fatal("mismatched request received stored response")
		}
	}

	{
		err := peer.AssertComplete(time.Second)
		if err == nil {
			t.Fatal("mismatched transcript was accepted")
		}
	}
}
