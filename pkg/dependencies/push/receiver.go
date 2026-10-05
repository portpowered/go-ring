// Package push adapts the long-lived FCM connection to Ring's account-scoped API.
package push

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	pushreceiver "github.com/portpowered/go-ring/third_party/go-push-receiver"
)

const (
	eventBufferSize = 16
	apiKey          = protocol.FCMAPIKey // #nosec G101 -- public FCM client key.
	projectID       = protocol.FCMProjectID
	appID           = protocol.FCMApplicationID
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

// Start opens one FCM receiver. Credentials are caller-owned and can be
// persisted between runs; the receiver itself owns no Ring account state.
func Start(ctx context.Context, saved json.RawMessage) (<-chan Event, error) {
	return StartWithHTTPTransport(ctx, saved, nil)
}

// StartWithHTTPTransport opens one FCM receiver using the supplied HTTP
// transport. A nil transport uses the standard HTTP transport.
func StartWithHTTPTransport(
	ctx context.Context,
	saved json.RawMessage,
	transport http.RoundTripper,
) (<-chan Event, error) {
	return StartWithTransports(ctx, saved, transport, nil)
}

// StartWithTransports opens the FCM receiver with injectable HTTP and MCS
// transports. The dial context function must return a connection ready for
// MCS frames; nil uses the receiver's TLS dialer.
func StartWithTransports(
	ctx context.Context,
	saved json.RawMessage,
	transport http.RoundTripper,
	dialContext DialContextFunc,
) (<-chan Event, error) {
	options := make([]pushreceiver.ClientOption, 0, 2)

	if len(saved) > 0 {
		var credentials pushreceiver.FCMCredentials

		err := json.Unmarshal(saved, &credentials)
		if err != nil {
			return nil, ringerrors.NewBadRequestError("decode saved FCM credentials", err)
		}

		if credentials.Token == "" {
			return nil, ringerrors.NewBadRequestError("saved FCM credentials have no token", nil)
		}

		options = append(options, pushreceiver.WithCreds(&credentials))
	}

	diagnostics := newDiagnosticTransport(transport)
	options = append(options, pushreceiver.WithHTTPClient(&http.Client{Transport: diagnostics}))

	if dialContext != nil {
		options = append(options, pushreceiver.WithMCSDialContext(pushreceiver.MCSDialContext(dialContext)))
	}

	client := pushreceiver.New(&pushreceiver.Config{
		ApiKey:    apiKey,
		ProjectID: projectID,
		AppID:     appID,
		VapidKey:  protocol.FCMDefaultVAPIDKey,
	}, options...)
	out := make(chan Event, eventBufferSize)

	go client.Subscribe(ctx)
	go func() {
		defer close(out)

		if len(saved) > 0 {
			var credentials pushreceiver.FCMCredentials

			_ = json.Unmarshal(saved, &credentials)

			if !send(ctx, out, Event{
				Kind:        KindCredentials,
				Token:       credentials.Token,
				Credentials: saved,
				Data:        nil,
				Err:         nil,
			}) {
				return
			}
		}

		for raw := range client.Events {
			if ctx.Err() != nil {
				return
			}

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

		_ = send(ctx, out, Event{
			Kind:        KindClosed,
			Token:       "",
			Credentials: nil,
			Data:        nil,
			Err:         nil,
		})
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

		return Event{
			Kind:        KindCredentials,
			Token:       value.Credentials.Token,
			Credentials: data,
			Data:        nil,
			Err:         err,
		}, true
	case *pushreceiver.ConnectedEvent:
		return Event{
			Kind:        KindConnected,
			Token:       "",
			Credentials: nil,
			Data:        nil,
			Err:         nil,
		}, true
	case *pushreceiver.MessageEvent:
		return Event{
			Kind:        KindMessage,
			Token:       "",
			Credentials: nil,
			Data:        value.Data,
			Err:         nil,
		}, true
	case *pushreceiver.RetryEvent:
		return Event{
			Kind:        KindRetry,
			Token:       "",
			Credentials: nil,
			Data:        nil,
			Err:         value.ErrorObj,
		}, true
	case *pushreceiver.UnauthorizedError:
		return Event{
			Kind:        KindRetry,
			Token:       "",
			Credentials: nil,
			Data:        nil,
			Err:         value.ErrorObj,
		}, true
	case *pushreceiver.DisconnectedEvent:
		return Event{
			Kind:        KindClosed,
			Token:       "",
			Credentials: nil,
			Data:        nil,
			Err:         nil,
		}, true
	default:
		return Event{
			Kind:        "",
			Token:       "",
			Credentials: nil,
			Data:        nil,
			Err:         nil,
		}, false
	}
}
