package replay_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type ticketFailureCase struct {
	Case   string `json:"case"`
	Status int    `json:"status"`
	Body   string `json:"body"`
	Error  string `json:"error"`
}

type ticketFailureTransport struct {
	caseData ticketFailureCase
	seen     bool
}

func (tr *ticketFailureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.seen = true
	if req.Method != http.MethodPost || req.URL.Path != "/api/v1/clap/ticket/request/signalsocket" {
		return nil, errors.New("unexpected ticket route")
	}
	if tr.caseData.Status == 0 {
		return nil, errors.New("recorded transport interruption")
	}
	body := tr.caseData.Body
	if body == "oversized" {
		body = strings.Repeat("x", (1<<20)+1)
	}
	return &http.Response{StatusCode: tr.caseData.Status, Status: http.StatusText(tr.caseData.Status), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestPortableLegacyTicketFailureResponses(t *testing.T) {
	cases, err := replay.LoadCases[ticketFailureCase](filepath.Join("fixtures", "http", "synthetic", "legacy-ticket-failures.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			transport := &ticketFailureTransport{caseData: tc}
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://solutions.example.test"}))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, got := client.OpenSignaling(context.Background(), ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "fixture-token"}})
			if !transport.seen || got == nil {
				t.Fatalf("ticket failure not reached: %v", got)
			}
			switch tc.Error {
			case "http":
				if !ringapimodels.IsHTTPError(got) {
					t.Fatalf("ticket error = %v", got)
				}
			case "bad-request":
				if !ringapimodels.IsBadRequestError(got) {
					t.Fatalf("ticket error = %v", got)
				}
			case "internal-server":
				if !ringapimodels.IsInternalServerError(got) {
					t.Fatalf("ticket error = %v", got)
				}
			case "connection":
				if !ringapimodels.IsConnectionError(got) {
					t.Fatalf("ticket error = %v", got)
				}
			case "network":
				if !ringapimodels.IsNetworkError(got) {
					t.Fatalf("ticket error = %v", got)
				}
			}
		})
	}
}
