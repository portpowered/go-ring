package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestLogoutRemovesPushCredentials(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "tokens.json")

	pushPath := filepath.Join(directory, "push.json")

	for _, path := range []string{tokenPath, pushPath} {
		err := os.WriteFile(path, []byte("{}"), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := run(
		context.Background(),
		[]string{"--token-file", tokenPath, "auth", "logout"},
		strings.NewReader(""),
		io.Discard,
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{tokenPath, pushPath} {
		_, err = os.Stat(path)
		if !os.IsNotExist(err) {
			t.Fatalf("%s still exists after logout: %v", path, err)
		}
	}
}

func TestEventsCommandConsumesPairedTranscriptThroughEOF(t *testing.T) {
	t.Parallel()

	sourceContexts := make(chan context.Context, 1)
	sourceDrained := make(chan struct{})
	transcript := []ring.FCMEvent{
		{
			Kind:        ring.PushCredentials,
			Token:       "synthetic-fcm-token",
			DeviceID:    "",
			Action:      "",
			Credentials: json.RawMessage(`{"token":"synthetic-fcm-token"}`),
			Data:        nil,
			Err:         nil,
		},
		{
			Kind:        ring.PushConnected,
			Token:       "",
			DeviceID:    "",
			Action:      "",
			Credentials: nil,
			Data:        nil,
			Err:         nil,
		},
		{
			Kind:        ring.PushClosed,
			Token:       "",
			DeviceID:    "",
			Action:      "",
			Credentials: nil,
			Data:        nil,
			Err:         nil,
		},
	}
	source := func(ctx context.Context, _ json.RawMessage) (<-chan ring.FCMEvent, error) {
		sourceContexts <- ctx

		stream := make(chan ring.FCMEvent)

		go func() {
			defer func() {
				close(stream)
				close(sourceDrained)
			}()

			for _, event := range transcript {
				select {
				case stream <- event:
				case <-ctx.Done():
					return
				}
			}
		}()

		return stream, nil
	}

	store, transport := newCLIEventStore(t, source)

	var output bytes.Buffer

	err := eventsCommand(context.Background(), store, []string{"watch", "1000", "--duration", "1s"}, &output)
	if err != nil {
		t.Fatal(err)
	}

	select {
	case <-sourceDrained:
	case <-time.After(time.Second):
		t.Fatal("event source did not deliver its complete transcript and EOF")
	}

	want := "FCM credentials saved\nFCM registered\nFCM connected\nFCM closed\n"
	if output.String() != want {
		t.Fatalf("event output = %q, want ordered transcript %q", output.String(), want)
	}

	select {
	case sourceCtx := <-sourceContexts:
		if !errors.Is(sourceCtx.Err(), context.Canceled) {
			t.Fatalf("source context after CLI Close = %v, want cancellation", sourceCtx.Err())
		}
	default:
		t.Fatal("push source was not started")
	}

	credentials, err := os.ReadFile(filepath.Join(filepath.Dir(store.path), "push.json"))
	if err != nil {
		t.Fatal(err)
	}

	if string(credentials) != `{"token":"synthetic-fcm-token"}` {
		t.Fatalf("saved push credentials = %s", credentials)
	}

	err = transport.AssertConsumed()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEventsCommandCancellationCompletesPushCleanup(t *testing.T) {
	t.Parallel()

	sourceContexts := make(chan context.Context, 1)
	sourceStopped := make(chan struct{})
	connected := make(chan struct{})
	source := func(ctx context.Context, _ json.RawMessage) (<-chan ring.FCMEvent, error) {
		sourceContexts <- ctx

		stream := make(chan ring.FCMEvent)

		go func() {
			defer func() {
				close(stream)
				close(sourceStopped)
			}()

			credentials := ring.FCMEvent{
				Kind:        ring.PushCredentials,
				Token:       "synthetic-fcm-token",
				DeviceID:    "",
				Action:      "",
				Credentials: json.RawMessage(`{"token":"synthetic-fcm-token"}`),
				Data:        nil,
				Err:         nil,
			}
			select {
			case stream <- credentials:
			case <-ctx.Done():
				return
			}

			select {
			case stream <- ring.FCMEvent{
				Kind:        ring.PushConnected,
				Token:       "",
				DeviceID:    "",
				Action:      "",
				Credentials: nil,
				Data:        nil,
				Err:         nil,
			}:
			case <-ctx.Done():
				return
			}

			close(connected)
			<-ctx.Done()
		}()

		return stream, nil
	}
	store, transport := newCLIEventStore(t, source)
	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	output := &cliEventOutput{
		mu:    sync.Mutex{},
		data:  bytes.Buffer{},
		lines: make(chan string, 8),
	}

	commandDone := make(chan error, 1)

	go func() {
		commandDone <- eventsCommand(ctx, store, []string{"watch", "1000", "--duration", "30s"}, output)
	}()

	waitCLIEventLines(t, output.lines, []string{"FCM credentials saved", "FCM registered", "FCM connected"})

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("push source did not deliver the connected event")
	}

	cancel()

	select {
	case err := <-commandDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CLI did not finish closing the push session after cancellation")
	}

	select {
	case <-sourceStopped:
	case <-time.After(time.Second):
		t.Fatal("push source did not stop after cancellation")
	}

	select {
	case sourceCtx := <-sourceContexts:
		if sourceCtx.Err() == nil {
			t.Fatal("push source context remained active after cancellation")
		}
	default:
		t.Fatal("push source was not started")
	}

	if got, want := output.String(), "FCM credentials saved\nFCM registered\nFCM connected\n"; got != want {
		t.Fatalf("event output = %q, want ordered transcript %q", got, want)
	}

	err := transport.AssertConsumed()
	if err != nil {
		t.Fatal(err)
	}
}

type cliEventOutput struct {
	mu    sync.Mutex
	data  bytes.Buffer
	lines chan string
}

func (output *cliEventOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	written, _ := output.data.Write(data)
	output.mu.Unlock()

	select {
	case output.lines <- strings.TrimSpace(string(data)):
	default:
	}

	return written, nil
}

func (output *cliEventOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()

	return output.data.String()
}

func waitCLIEventLines(t *testing.T, lines <-chan string, expected []string) {
	t.Helper()

	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()

	for _, want := range expected {
		select {
		case got := <-lines:
			if got != want {
				t.Fatalf("event output line = %q, want %q", got, want)
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for CLI event output %q", want)
		}
	}
}

func newCLIEventStore(t *testing.T, source ring.FCMSource) (tokenStore, *replay.Transport) {
	t.Helper()

	fixtures := []string{"ring-session-register", "ring-push-register", "ring-push-motion-subscribe"}
	exchanges := make([]replay.Exchange, 0, len(fixtures))

	for _, name := range fixtures {
		path := filepath.Join("..", "..", "tests", "replay", "fixtures", "http", "synthetic", name+".json")

		exchange, err := replay.LoadExchange(path)
		if err != nil {
			t.Fatal(err)
		}

		exchanges = append(exchanges, exchange)
	}

	transport := replay.NewTransport(exchanges...)
	store := tokenStore{
		path: filepath.Join(t.TempDir(), "tokens.json"),
		clientOptions: []ring.Option{
			ring.WithHTTPClient(&http.Client{Transport: transport}),
			ring.WithFCMSource(source),
		},
	}

	err := store.save(storedTokens{
		AuthResponse: ringapimodels.AuthResponse{
			AccessToken: "synthetic-access-token", RefreshToken: "synthetic-refresh-token",
			ExpiresIn: 3600, TokenType: "Bearer",
		},
		HardwareID: "synthetic-hardware-id",
		ReceivedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	return store, transport
}
