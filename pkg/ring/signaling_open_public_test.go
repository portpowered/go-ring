package ring_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

func TestOpenSignalingRejectsLocalPreconditions(t *testing.T) {
	t.Parallel()

	client, err := ring.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	{
		_, err := client.OpenSignaling(
			ctx,
			ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "token", HardwareID: ""}},
		)
		if !errors.Is(
			err,
			context.Canceled,
		) {
			t.Fatalf("canceled OpenSignaling error = %v", err)
		}
	}

	noToken, err := ring.NewClient()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = noToken.Close() })

	{
		_, err := noToken.OpenSignaling(context.Background(), ring.OpenSignalingRequest{})
		if err == nil {
			t.Fatal("OpenSignaling succeeded without an access token")
		}
	}

	unverified, err := ring.NewClient(ring.WithRegion(ring.RegionEU))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = unverified.Close() })

	{
		_, err := unverified.OpenSignaling(
			context.Background(),
			ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "token", HardwareID: ""}},
		)
		if err == nil ||
			!strings.Contains(err.Error(), "unverified") {
			t.Fatalf("EU bootstrap error = %v; want unverified endpoint error", err)
		}
	}
}

func TestOpenSignalingRejectsMalformedOrEmptyTicketResponse(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body string }{
		{"malformed", `{"ticket":`},
		{"empty", `{"ticket":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("ticket method = %s", r.Method)
				}

				if r.Header.Get("Authorization") != "Bearer access-token" {
					t.Errorf("authorization header not sent")
				}

				responseWriter.Header().Set("Content-Type", "application/json")
				_, _ = responseWriter.Write([]byte(tc.body))
			}))
			t.Cleanup(srv.Close)

			client, err := ring.NewClient(
				ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: srv.URL}),
				ring.WithSignalingWebSocketURL("wss://example.invalid/{token}"),
			)
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = client.Close() })

			{
				_, err := client.OpenSignaling(
					context.Background(),
					ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "access-token", HardwareID: ""}},
				)
				if err == nil {
					t.Fatal("invalid ticket response was accepted")
				}
			}
		})
	}
}

func TestOpenSignalingHTTPFailureDoesNotExposeResponseBody(t *testing.T) {
	t.Parallel()

	const secret = "private-ticket-material"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, secret, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	client, err := ring.NewClient(ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: srv.URL}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = client.Close() })

	_, err = client.OpenSignaling(
		context.Background(),
		ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "token", HardwareID: ""}},
	)
	if err == nil {
		t.Fatal("OpenSignaling accepted HTTP 500")
	}

	if strings.Contains(err.Error(), secret) {
		t.Fatal("signaling error exposed response body")
	}
}
