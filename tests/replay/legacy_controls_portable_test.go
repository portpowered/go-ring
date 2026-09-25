package replay_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
)

type portableControlCase struct {
	Case     string          `json:"case"`
	Request  replay.Request  `json:"request"`
	Response replay.Response `json:"response"`
}

type portableChimeCase struct {
	Case    string `json:"case"`
	Request struct {
		Method string         `json:"method"`
		Path   string         `json:"path"`
		Query  map[string]any `json:"query"`
	} `json:"request"`
	Response replay.Response `json:"response"`
}

// Every Python legacy control case is replayed through the corresponding Go
// public API. The siren and motion cases also match separate C1 captures.
func TestPortableLegacyHTTPControls(t *testing.T) {
	cases, err := replay.LoadCases[portableControlCase](filepath.Join("fixtures", "porting", "legacy-control-requests.json"))
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, tc := range cases {
		matched++
		t.Run(tc.Case, func(t *testing.T) {
			const origin = "https://portable.example.test"
			tc.Request.Origin = origin
			tc.Request.HeadersMode = replay.HeadersRequired
			if bytes.Equal(bytes.TrimSpace(tc.Request.Body), []byte("null")) {
				tc.Request.JSON = false
			}
			for _, placeholder := range []string{"{camera_id}", "{doorbell_id}", "{chime_id}"} {
				tc.Request.Path = strings.ReplaceAll(tc.Request.Path, placeholder, "12345")
			}
			x := replay.Exchange{Request: tc.Request, Response: tc.Response}
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin}))
			if err != nil {
				t.Fatal(err)
			}
			switch tc.Case {
			case "chime-volume":
				err = client.SetVolume(context.Background(), ring.SetVolumeRequest{DeviceID: "12345", Kind: "chime", Description: "Fixture Device", Volume: 2})
			case "doorbell-volume":
				err = client.SetVolume(context.Background(), ring.SetVolumeRequest{DeviceID: "12345", Kind: "doorbell", Description: "Fixture Device", Volume: 3})
			case "chime-test":
				err = client.TestSound(context.Background(), ring.TestSoundRequest{DeviceID: "12345", Kind: "ding"})
			case "camera-light-on":
				err = client.SetLights(context.Background(), ring.SetLightsRequest{DeviceID: "12345", State: "on"})
			case "camera-siren-off":
				err = client.SetSiren(context.Background(), ring.SetSirenRequest{DeviceID: "12345", Enabled: false})
			case "doorbell-motion-on":
				err = client.SetMotionDetection(context.Background(), ring.SetMotionDetectionRequest{DeviceID: "12345", Enabled: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := transport.AssertConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if matched != 6 {
		t.Fatalf("portable controls: got %d cases, want 6", matched)
	}
}

func TestPortableInHomeChimeOptions(t *testing.T) {
	cases, err := replay.LoadCases[portableChimeCase](filepath.Join("fixtures", "porting", "legacy-in-home-chime.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 3 {
		t.Fatalf("expected three portable chime cases, got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Case, func(t *testing.T) {
			const origin = "https://portable.example.test"
			request := replay.Request{Method: tc.Request.Method, Origin: origin, Path: strings.ReplaceAll(tc.Request.Path, "{doorbell_id}", "12345"), HeadersMode: replay.HeadersRequired}
			for key, value := range tc.Request.Query {
				request.Query = append(request.Query, replay.Pair{Name: key, Value: fmt.Sprint(value)})
			}
			transport := replay.NewTransport(replay.Exchange{Request: request, Response: tc.Response})
			client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin}))
			if err != nil {
				t.Fatal(err)
			}
			settings := map[string]interface{}{}
			switch tc.Case {
			case "type":
				settings["type"] = 1
			case "enabled":
				settings["enabled"] = false
			case "duration":
				settings["duration"] = 5
			default:
				t.Fatalf("unknown portable case %s", tc.Case)
			}
			if err := client.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{DeviceID: "12345", Description: "Fixture Device", Settings: settings}); err != nil {
				t.Fatal(err)
			}
			if err := transport.AssertConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
