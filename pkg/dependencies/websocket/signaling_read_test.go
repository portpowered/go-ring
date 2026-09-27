package websocket

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/signaling"
)

func TestReadSignalingPreservesDecoderAndCloseCauses(t *testing.T) {
	cases := []struct {
		name      string
		frameType int
		payload   []byte
		check     func(error) bool
	}{
		{"invalid JSON", websocket.TextMessage, []byte(`{"method":`), func(err error) bool { var syntax *json.SyntaxError; return errors.As(err, &syntax) }},
		{"socket close", websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), func(err error) bool { var closeErr *websocket.CloseError; return errors.As(err, &closeErr) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				peer, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = peer.Close() }()
				_ = peer.WriteMessage(tc.frameType, tc.payload)
			}))
			defer server.Close()
			conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			err = ReadSignaling(conn, func(signaling.Message) { t.Fatal("unexpected valid message") })
			if !ringerrors.IsConnectionError(err) {
				t.Fatalf("read error is not typed: %v", err)
			}
			if !tc.check(err) {
				t.Fatalf("read error lost cause: %v", err)
			}
		})
	}
}
