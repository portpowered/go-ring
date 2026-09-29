package mocks_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/mocks"
)

func TestQueueJSONMessagePreservesMarshalCause(t *testing.T) {
	t.Parallel()

	connection := mocks.NewMockWebSocketConn()
	err := connection.QueueJSONMessage(func() {})

	var typeError *json.UnsupportedTypeError
	if !errors.As(err, &typeError) {
		t.Fatalf("JSON queue error lost marshal cause: %v", err)
	}
}
