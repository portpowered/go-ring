package replay

import (
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
