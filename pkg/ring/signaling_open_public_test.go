package ring

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenSignalingRejectsLocalPreconditions(t *testing.T) {
	client, err := NewClient(WithAccessToken("token"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.OpenSignaling(ctx, OpenSignalingRequest{}); err != context.Canceled {
		t.Fatalf("canceled OpenSignaling error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.OpenSignaling(context.Background(), OpenSignalingRequest{}); err == nil {
		t.Fatal("OpenSignaling succeeded on a closed client")
	}

	noToken, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer noToken.Close()
	if _, err := noToken.OpenSignaling(context.Background(), OpenSignalingRequest{}); err == nil {
		t.Fatal("OpenSignaling succeeded without an access token")
	}

	unverified, err := NewClient(WithAccessToken("token"), WithRegion(RegionEU))
	if err != nil {
		t.Fatal(err)
	}
	defer unverified.Close()
	if _, err := unverified.OpenSignaling(context.Background(), OpenSignalingRequest{}); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("EU bootstrap error = %v; want unverified endpoint error", err)
	}
}

func TestOpenSignalingRejectsMalformedOrEmptyTicketResponse(t *testing.T) {
	cases := []struct{ name, body string }{
		{"malformed", `{"ticket":`},
		{"empty", `{"ticket":""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					t.Errorf("ticket method = %s", r.Method)
				}
				if r.Header.Get("Authorization") != "Bearer access-token" {
					t.Errorf("authorization header not sent")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			client, err := NewClient(WithAccessToken("access-token"), WithEndpoints(Endpoints{SolutionsBaseURL: srv.URL}), WithRTCWebSocketURL("wss://example.invalid/{token}"))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.OpenSignaling(context.Background(), OpenSignalingRequest{}); err == nil {
				t.Fatal("invalid ticket response was accepted")
			}
		})
	}
}

func TestOpenSignalingHTTPFailureDoesNotExposeResponseBody(t *testing.T) {
	const secret = "private-ticket-material"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, secret, http.StatusInternalServerError)
	}))
	defer srv.Close()
	client, err := NewClient(WithAccessToken("access-token"), WithEndpoints(Endpoints{SolutionsBaseURL: srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.OpenSignaling(context.Background(), OpenSignalingRequest{})
	if err == nil {
		t.Fatal("OpenSignaling accepted HTTP 500")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("signaling error exposed response body")
	}
}
