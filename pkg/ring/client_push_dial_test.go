package ring

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	checkinPB "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/checkin"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type pushDialCheckinRoundTripper func(*http.Request) (*http.Response, error)

type offlineDialError struct{}

func (offlineDialError) Error() string { return "synthetic Ring MCS endpoint is offline" }

func (transport pushDialCheckinRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestWithFCMDialContextReachesBuiltInReceiver(t *testing.T) {
	t.Parallel()

	type dialCall struct{ network, address string }

	dialCalls := make(chan dialCall, 1)
	dialContext := FCMDialContext(func(_ context.Context, network, address string) (net.Conn, error) {
		dialCalls <- dialCall{network: network, address: address}

		return nil, offlineDialError{}
	})

	client, err := NewClient(WithFCMDialContext(dialContext))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	checkinResponse, err := proto.Marshal(&checkinPB.AndroidCheckinResponse{StatsOk: proto.Bool(true)})
	require.NoError(t, err)

	requestURLs := make(chan string, 1)
	transport := pushDialCheckinRoundTripper(func(request *http.Request) (*http.Response, error) {
		requestURLs <- request.URL.String()

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(checkinResponse)),
			Request:    request,
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	saved := json.RawMessage(`{"androidId":12345,"securityToken":67890,"token":"saved-token"}`)
	events, err := defaultFCMSource(ctx, saved, transport, client.fcmDialContext)
	require.NoError(t, err)

	var retryReceived bool

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("built-in receiver closed before invoking the configured MCS dialer")
			}

			if event.Kind == PushRetry {
				retryReceived = true

				cancel()
			}
		case <-ctx.Done():
			t.Fatal("built-in receiver did not invoke the configured MCS dialer")
		}

		if retryReceived {
			break
		}
	}

	for range events {
	}

	select {
	case requestURL := <-requestURLs:
		require.Equal(t, "https://"+protocol.FCMCheckinHost+protocol.FCMCheckinPath, requestURL)
	default:
		t.Fatal("built-in receiver did not use the configured HTTP transport")
	}

	select {
	case call := <-dialCalls:
		require.Equal(t, protocol.MCSNetwork, call.network)
		require.Equal(t, net.JoinHostPort(protocol.MCSHost, protocol.MCSPort), call.address)
	default:
		t.Fatal("Ring's FCM dial context option did not reach the MCS receiver")
	}
}
