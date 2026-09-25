package system_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type portableMedia struct {
	Recording struct {
		BodyText string `json:"body_text"`
		ShareURL string `json:"share_url"`
	} `json:"recording"`
	Snapshot struct {
		TimestampMS int64  `json:"timestamp_ms"`
		BodyText    string `json:"body_text"`
	} `json:"snapshot"`
}

func TestPortableRecordingBytes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "porting-fixtures", "media-variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	var media portableMedia
	if err := json.Unmarshal(b, &media); err != nil {
		t.Fatal(err)
	}
	if media.Recording.BodyText == "" || media.Recording.ShareURL == "" || media.Snapshot.BodyText == "" {
		t.Fatal("incomplete shared media fixture")
	}
	const origin = "https://portable.example.test"
	responseBody, _ := json.Marshal(media.Recording.BodyText)
	transport := replay.NewTransport(replay.Exchange{
		Request:  replay.Request{Method: "GET", Origin: origin, Path: "/clients_api/dings/42/recording", Headers: http.Header{"Accept": []string{"video/mp4,*/*"}}, HeadersMode: replay.HeadersRequired},
		Response: replay.Response{Status: 200, Headers: http.Header{"Content-Type": []string{"video/mp4"}}, Body: responseBody},
	})
	client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin}))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.GetRecording(context.Background(), ring.GetRecordingRequest{RecordingID: 42})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	got, err := io.ReadAll(stream.Body)
	if err != nil || string(got) != media.Recording.BodyText || stream.ContentType != "video/mp4" {
		t.Fatalf("recording stream = %q, %s, %v", got, stream.ContentType, err)
	}
	if err := transport.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

type portableFailure struct {
	Case    string `json:"case"`
	Outcome string `json:"outcome"`
	Status  int    `json:"status"`
}
type failureTransport struct {
	status int
	err    error
}

func (f failureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: f.status, Status: http.StatusText(f.status), Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}, Request: r}, nil
}

func TestPortableHTTPFailures(t *testing.T) {
	cases, err := replay.LoadCases[portableFailure](filepath.Join("..", "porting-fixtures", "http-failures.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatalf("expected four shared failure cases, got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			f := failureTransport{status: tc.Status}
			if tc.Outcome == "timeout" {
				f.err = context.DeadlineExceeded
			} else if tc.Outcome != "" {
				f.err = errors.New(tc.Outcome)
			}
			client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: f}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://portable.example.test"}))
			if err != nil {
				t.Fatal(err)
			}
			err = client.SetSiren(context.Background(), ring.SetSirenRequest{DeviceID: "12345", Enabled: false})
			if err == nil {
				t.Fatal("transport failure was accepted")
			}
			if tc.Status > 0 {
				if !ringapimodels.IsHTTPError(err) {
					t.Fatalf("status %d error = %v", tc.Status, err)
				}
			} else if !ringapimodels.IsNetworkError(err) {
				t.Fatalf("transport error = %v", err)
			}
		})
	}
}
