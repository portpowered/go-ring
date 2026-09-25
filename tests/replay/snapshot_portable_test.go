package replay_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
)

func snapshotExchanges(t *testing.T, timestamp int64, withImage bool) ([]replay.Exchange, portableMedia) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("fixtures", "porting", "media-variants.json"))
	if err != nil {
		t.Fatal(err)
	}
	var media portableMedia
	if err := json.Unmarshal(b, &media); err != nil {
		t.Fatal(err)
	}
	const origin = "https://portable.example.test"
	postBody := json.RawMessage(`{"doorbot_ids":[12345]}`)
	post := func(body json.RawMessage) replay.Exchange {
		return replay.Exchange{
			Request:  replay.Request{Method: "POST", Origin: origin, Path: "/clients_api/snapshots/timestamps", HeadersMode: replay.HeadersRequired, Headers: http.Header{"Authorization": []string{"Bearer portable-token"}}, Body: postBody, JSON: true},
			Response: replay.Response{Status: 200, Headers: http.Header{"Content-Type": []string{"application/json"}}, Body: body, JSON: true},
		}
	}
	pollBody, _ := json.Marshal(map[string]any{"timestamps": []map[string]int64{{"timestamp": timestamp}}})
	exchanges := []replay.Exchange{post(json.RawMessage(`{}`)), post(pollBody)}
	if withImage {
		imageBody, _ := json.Marshal(media.Snapshot.BodyText)
		exchanges = append(exchanges, replay.Exchange{
			Request:  replay.Request{Method: "GET", Origin: origin, Path: "/clients_api/snapshots/image/12345", HeadersMode: replay.HeadersRequired, Headers: http.Header{"Accept": []string{"image/jpeg"}, "Authorization": []string{"Bearer portable-token"}}},
			Response: replay.Response{Status: 200, Headers: http.Header{"Content-Type": []string{"image/jpeg"}}, Body: imageBody},
		})
	}
	return exchanges, media
}

func TestPortableSnapshotFreshnessAndImage(t *testing.T) {
	exchanges, media := snapshotExchanges(t, 9_000_000_000_000, true)
	transport := replay.NewTransport(exchanges...)
	client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://portable.example.test"}))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.GetSnapshot(context.Background(), ring.GetSnapshotRequest{DeviceID: "12345"})
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot.Bytes) != media.Snapshot.BodyText || snapshot.Timestamp.UnixMilli() != media.Snapshot.TimestampMS || snapshot.ContentType != "image/jpeg" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if err := transport.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestPortableSnapshotStaleTimestamp(t *testing.T) {
	exchanges, _ := snapshotExchanges(t, 1, false)
	transport := replay.NewTransport(exchanges...)
	client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://portable.example.test"}))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.GetSnapshot(context.Background(), ring.GetSnapshotRequest{DeviceID: "12345", MaxAttempts: 1})
	if snapshot != nil || !errors.Is(err, ring.ErrSnapshotNotReady) {
		t.Fatalf("stale snapshot = %+v, %v", snapshot, err)
	}
	if err := transport.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestPortableSnapshotMissingTimestampThenFresh(t *testing.T) {
	exchanges, media := snapshotExchanges(t, 9_000_000_000_000, true)
	empty := exchanges[1]
	empty.Response.Body = json.RawMessage(`{}`)
	exchanges = append(exchanges[:1], append([]replay.Exchange{empty}, exchanges[1:]...)...)
	transport := replay.NewTransport(exchanges...)
	client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://portable.example.test"}))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.GetSnapshot(context.Background(), ring.GetSnapshotRequest{DeviceID: "12345", MaxAttempts: 2})
	if err != nil || string(snapshot.Bytes) != media.Snapshot.BodyText {
		t.Fatalf("delayed snapshot = %+v, %v", snapshot, err)
	}
	if err := transport.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestPortableSnapshotInvalidRequestsBeforeHTTP(t *testing.T) {
	transport := replay.NewTransport()
	client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []ring.GetSnapshotRequest{
		{DeviceID: "0"}, {DeviceID: "bad"}, {DeviceID: "12345", MaxAttempts: -1}, {DeviceID: "12345", MaxAttempts: 101}, {DeviceID: "12345", PollInterval: -time.Second},
	} {
		if snapshot, err := client.GetSnapshot(context.Background(), req); snapshot != nil || err == nil {
			t.Fatalf("invalid snapshot request %+v returned %+v, %v", req, snapshot, err)
		}
	}
	if err := transport.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestPortableSnapshotImageFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   json.RawMessage
	}{{"forbidden", 403, json.RawMessage(`""`)}, {"empty image", 200, json.RawMessage(`""`)}} {
		t.Run(tc.name, func(t *testing.T) {
			exchanges, _ := snapshotExchanges(t, 9_000_000_000_000, true)
			exchanges[2].Response.Status = tc.status
			exchanges[2].Response.Body = tc.body
			transport := replay.NewTransport(exchanges...)
			client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://portable.example.test"}))
			if err != nil {
				t.Fatal(err)
			}
			if snapshot, err := client.GetSnapshot(context.Background(), ring.GetSnapshotRequest{DeviceID: "12345", MaxAttempts: 1}); snapshot != nil || err == nil {
				t.Fatalf("bad image returned %+v, %v", snapshot, err)
			}
			if err := transport.AssertConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

type signalFirstTransport struct {
	inner  *replay.Transport
	first  chan struct{}
	called bool
}

func (s *signalFirstTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := s.inner.RoundTrip(r)
	if !s.called {
		s.called = true
		close(s.first)
	}
	return resp, err
}

func TestPortableSnapshotCancellationDuringPoll(t *testing.T) {
	exchanges, _ := snapshotExchanges(t, 1, false)
	inner := replay.NewTransport(exchanges[0])
	transport := &signalFirstTransport{inner: inner, first: make(chan struct{})}
	client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: "https://portable.example.test"}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.GetSnapshot(ctx, ring.GetSnapshotRequest{DeviceID: "12345", MaxAttempts: 1, PollInterval: time.Hour})
		done <- err
	}()
	select {
	case <-transport.first:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("snapshot trigger was not sent")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot polling did not stop")
	}
	if err := inner.AssertConsumed(); err != nil {
		t.Fatal(err)
	}
}
