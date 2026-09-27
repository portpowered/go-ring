// Package push adapts the long-lived FCM connection to Ring's account-scoped API.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	pushreceiver "github.com/crow-misia/go-push-receiver"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

const (
	eventBufferSize = 16
	apiKey          = "AIzaSyCv-hdFBmmdBBJadNy-TFwB-xN_H5m3Bk8" // #nosec G101 -- public Android app API key required by FCM registration.
	projectID       = "ring-17770"
	appID           = "1:876313859327:android:e10ec6ddb3c81f39"
)

type Kind string

const (
	KindCredentials Kind = "credentials"
	KindConnected   Kind = "connected"
	KindMessage     Kind = "message"
	KindRetry       Kind = "retry"
	KindClosed      Kind = "closed"
)

type Event struct {
	Kind        Kind
	Token       string
	Credentials json.RawMessage
	Data        json.RawMessage
	Err         error
}

type diagnosticTransport struct {
	failed atomic.Value
	next   http.RoundTripper
}

// NewRegistrationTransport applies the compatibility rules required by Ring's
// FCM registration before handing a request to the supplied transport.
func NewRegistrationTransport(next http.RoundTripper) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}
	return &diagnosticTransport{next: next}
}

func (t *diagnosticTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "fcmregistrations.googleapis.com" {
		req.Header.Set("x-goog-firebase-installations-auth", strings.TrimPrefix(req.Header.Get("x-goog-firebase-installations-auth"), "FIS "))
		if err := omitDefaultVAPID(req); err != nil {
			return nil, err
		}
	}
	response, err := t.next.RoundTrip(req)
	if response != nil && response.StatusCode >= http.StatusBadRequest {
		t.failed.Store(req.URL.Host + req.URL.Path)
	} else if err == nil {
		t.failed.Store("")
	}
	return response, err
}

// FCM rejects the default Web Push key when it is sent explicitly. The
// upstream Go receiver always includes it; the reference receiver omits it.
func omitDefaultVAPID(req *http.Request) error {
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	var body struct {
		Web map[string]json.RawMessage `json:"web"`
	}
	if err = json.Unmarshal(data, &body); err != nil {
		return err
	}
	delete(body.Web, "applicationPubKey")
	data, err = json.Marshal(body)
	if err != nil {
		return err
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	req.ContentLength = int64(len(data))
	return nil
}

// Start opens one FCM receiver. Credentials are caller-owned and can be
// persisted between runs; the receiver itself owns no Ring account state.
func Start(ctx context.Context, saved json.RawMessage) (<-chan Event, error) {
	options := make([]pushreceiver.ClientOption, 0, 1)
	if len(saved) > 0 {
		var credentials pushreceiver.FCMCredentials
		if err := json.Unmarshal(saved, &credentials); err != nil {
			return nil, err
		}
		if credentials.Token == "" {
			return nil, ringerrors.NewBadRequestError("saved FCM credentials have no token", nil)
		}
		options = append(options, pushreceiver.WithCreds(&credentials))
	}
	diagnostics := NewRegistrationTransport(nil).(*diagnosticTransport)
	options = append(options, pushreceiver.WithHTTPClient(&http.Client{Transport: diagnostics}))
	client := pushreceiver.New(&pushreceiver.Config{ApiKey: apiKey, ProjectID: projectID, AppID: appID}, options...)
	out := make(chan Event, eventBufferSize)
	go client.Subscribe(ctx)
	go func() {
		defer close(out)
		if len(saved) > 0 {
			var credentials pushreceiver.FCMCredentials
			_ = json.Unmarshal(saved, &credentials)
			if !send(ctx, out, Event{Kind: KindCredentials, Token: credentials.Token, Credentials: saved}) {
				return
			}
		}
		for raw := range client.Events {
			event, ok := TranslateEvent(raw)
			if event.Err != nil {
				if stage, valid := diagnostics.failed.Load().(string); valid && stage != "" {
					event.Err = ringerrors.NewConnectionError("FCM "+stage+" failed", event.Err)
				}
			}
			if ok && !send(ctx, out, event) {
				return
			}
		}
		_ = send(ctx, out, Event{Kind: KindClosed})
	}()
	return out, nil
}

func send(ctx context.Context, out chan<- Event, event Event) bool {
	select {
	case out <- event:
		return true
	case <-ctx.Done():
		return false
	}
}

// TranslateEvent maps third-party receiver events into this package's event shape.
func TranslateEvent(raw pushreceiver.Event) (Event, bool) {
	switch value := raw.(type) {
	case *pushreceiver.UpdateCredentialsEvent:
		data, err := json.Marshal(value.Credentials)
		return Event{Kind: KindCredentials, Token: value.Credentials.Token, Credentials: data, Err: err}, true
	case *pushreceiver.ConnectedEvent:
		return Event{Kind: KindConnected}, true
	case *pushreceiver.MessageEvent:
		return Event{Kind: KindMessage, Data: value.Data}, true
	case *pushreceiver.RetryEvent:
		return Event{Kind: KindRetry, Err: value.ErrorObj}, true
	case *pushreceiver.UnauthorizedError:
		return Event{Kind: KindRetry, Err: value.ErrorObj}, true
	case *pushreceiver.DisconnectedEvent:
		return Event{Kind: KindClosed}, true
	default:
		return Event{}, false
	}
}
