package replay_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
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
			exchanges := []replay.Exchange{x}
			if tc.Case == "chime-volume" || tc.Case == "doorbell-volume" {
				kind := "chime"
				if tc.Case == "doorbell-volume" {
					kind = "doorbell"
				}
				body, err := json.Marshal(map[string]any{"devices": []map[string]any{{"id": 12345, "kind": kind, "description": "Fixture Device"}}})
				if err != nil {
					t.Fatal(err)
				}
				exchanges = append(exchanges, replay.Exchange{Request: replay.Request{Method: http.MethodGet, Origin: origin, Path: "/device_info/v3/devices", HeadersMode: replay.HeadersRequired}, Response: replay.Response{Status: http.StatusOK, Body: body, JSON: true}})
			}
			transport := replay.NewTransport(exchanges...)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin}))
			if err != nil {
				t.Fatal(err)
			}
			switch tc.Case {
			case "chime-volume":
				err = client.SetVolume(context.Background(), ring.SetVolumeRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Volume: 2})
			case "doorbell-volume":
				err = client.SetVolume(context.Background(), ring.SetVolumeRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Volume: 3})
			case "chime-test":
				err = client.TestSound(context.Background(), ring.TestSoundRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Sound: "ding"})
			case "camera-light-on":
				err = client.SetLights(context.Background(), ring.SetLightsRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Enabled: true})
			case "camera-siren-off":
				err = client.SetSiren(context.Background(), ring.SetSirenRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Enabled: false})
			case "doorbell-motion-on":
				err = client.SetMotionDetection(context.Background(), ring.SetMotionDetectionRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Enabled: true})
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
			listBody := []byte(`{"devices":[{"id":12345,"kind":"doorbell","description":"Fixture Device"}]}`)
			transport := replay.NewTransport(replay.Exchange{Request: request, Response: tc.Response}, replay.Exchange{Request: replay.Request{Method: http.MethodGet, Origin: origin, Path: "/device_info/v3/devices", HeadersMode: replay.HeadersRequired}, Response: replay.Response{Status: http.StatusOK, Body: listBody, JSON: true}})
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin}))
			if err != nil {
				t.Fatal(err)
			}
			settings := ringapimodels.InHomeChimeSettings{}
			switch tc.Case {
			case "type":
				settings.Type = chimePointer(1)
			case "enabled":
				settings.Enabled = chimePointer(false)
			case "duration":
				settings.Duration = chimePointer(5)
			default:
				t.Fatalf("unknown portable case %s", tc.Case)
			}
			if err := client.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Settings: settings}); err != nil {
				t.Fatal(err)
			}
			if err := transport.AssertConsumed(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControlDeviceResolutionReplay(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		call     func(*ring.Client) error
		notFound bool
	}{
		{"volume missing device", `{"devices":[]}`, func(c *ring.Client) error {
			return c.SetVolume(context.Background(), ring.SetVolumeRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Volume: 2})
		}, true},
		{"volume unsupported camera", `{"devices":[{"id":12345,"kind":"stickup_cam_mini_ptz_v1","description":"Camera"}]}`, func(c *ring.Client) error {
			return c.SetVolume(context.Background(), ring.SetVolumeRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Volume: 2})
		}, false},
		{"chime unsupported camera", `{"devices":[{"id":12345,"kind":"stickup_cam_mini_ptz_v1","description":"Camera"}]}`, func(c *ring.Client) error {
			return c.SetInHomeChime(context.Background(), ring.SetInHomeChimeRequest{Auth: ring.AuthContext{AccessToken: "portable-token"}, DeviceID: "12345", Settings: ringapimodels.InHomeChimeSettings{Enabled: chimePointer(true)}})
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const origin = "https://portable.example.test"
			transport := replay.NewTransport(replay.Exchange{
				Request:  replay.Request{Method: http.MethodGet, Origin: origin, Path: "/device_info/v3/devices", HeadersMode: replay.HeadersRequired},
				Response: replay.Response{Status: http.StatusOK, Body: []byte(tc.body), JSON: true},
			})
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{APIBaseURL: origin}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			err = tc.call(client)
			require.Error(t, err)
			if tc.notFound {
				require.True(t, ringapimodels.IsNotFoundError(err))
			} else {
				require.True(t, ringapimodels.IsBadRequestError(err))
			}
			require.NoError(t, transport.AssertConsumed())
		})
	}
}
