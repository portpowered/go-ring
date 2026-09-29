package replay_test

import (
	"context"
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
		return nil, testReplayError("unexpected ticket route")
	}

	if tr.caseData.Status == 0 {
		return nil, testReplayError("recorded transport interruption")
	}

	body := tr.caseData.Body
	if body == "oversized" {
		body = strings.Repeat("x", (1<<20)+1)
	}

	return &http.Response{
		StatusCode: tr.caseData.Status,
		Status:     http.StatusText(tr.caseData.Status),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestPortableLegacyTicketFailureResponses(t *testing.T) {
	t.Parallel()

	cases, err := replay.LoadCases[ticketFailureCase](
		filepath.Join("fixtures", "http", "synthetic", "legacy-ticket-failures.json"),
	)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			t.Parallel()
			assertPortableTicketFailure(t, tc)
		})
	}
}

func assertPortableTicketFailure(t *testing.T, tc ticketFailureCase) {
	t.Helper()

	transport := &ticketFailureTransport{caseData: tc}

	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://solutions.example.test"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = client.Close() }()

	_, got := client.OpenSignaling(
		context.Background(),
		ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: "fixture-token", HardwareID: ""}},
	)
	if !transport.seen || got == nil {
		t.Fatalf("ticket failure not reached: %v", got)
	}

	assertTicketErrorKind(t, tc.Error, got)
}

func assertTicketErrorKind(t *testing.T, want string, got error) {
	t.Helper()

	switch want {
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
}
