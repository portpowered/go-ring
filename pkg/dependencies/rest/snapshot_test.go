package rest

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestSnapshotImageNeedsTokenBeforeHTTP(t *testing.T) {
	t.Parallel()

	transport := replay.NewTransport()
	client := NewClient(WithHTTPClient(&http.Client{Transport: transport}))

	data, _, err := client.GetSnapshotImage(context.Background(), 12345)
	if data != nil || !ringapimodels.IsTokenError(err) {
		t.Fatalf("missing-token snapshot = %q, %v", data, err)
	}

	{
		err := transport.AssertConsumed()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotImageBoundsAndReadErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		body        io.ReadCloser
		wantNetwork bool
	}{
		{"oversize", io.NopCloser(strings.NewReader(strings.Repeat("x", maxSnapshotBytes+1))), false},
		{
			"read failure",
			&trackedBody{
				reader: strings.NewReader(""), closed: false, readErr: restTestError("synthetic read failure"),
			},
			true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client := NewClient(
				WithHTTPClient(
					&http.Client{
						Transport: roundTripFunc(
							func(r *http.Request) (*http.Response, error) { return testResponse(r, 200, tc.body), nil },
						),
					},
				),
			)

			data, _, err := client.GetSnapshotImage(authenticatedContext(), 12345)
			if data != nil || err == nil {
				t.Fatalf("invalid image returned %d bytes, %v", len(data), err)
			}

			if tc.wantNetwork && !ringapimodels.IsNetworkError(err) {
				t.Fatalf("read failure = %v", err)
			}

			if !tc.wantNetwork && !ringapimodels.IsInternalServerError(err) {
				t.Fatalf("oversize response = %v", err)
			}
		})
	}
}

func TestSnapshotTimestampAndImageTransportFailures(t *testing.T) {
	t.Parallel()

	client := NewClient(
		WithHTTPClient(
			&http.Client{
				Transport: roundTripFunc(
					func(r *http.Request) (*http.Response, error) {
						return nil, restTestError("synthetic transport failure")
					},
				),
			},
		),
	)
	{
		_, err := client.RefreshSnapshotTimestamp(authenticatedContext(), 12345)
		if !ringapimodels.IsNetworkError(err) {
			t.Fatalf("timestamp error = %v", err)
		}
	}

	{
		_, _, err := client.GetSnapshotImage(authenticatedContext(), 12345)
		if !ringapimodels.IsNetworkError(err) {
			t.Fatalf("image error = %v", err)
		}
	}
}
