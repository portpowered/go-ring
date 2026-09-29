package rest_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

type requestBuildCase struct {
	name string
	call func(*rest.Client, context.Context) error
}

type unexpectedRequestTransport struct {
	calls int
}

type unexpectedRequestError struct{}

func (unexpectedRequestError) Error() string { return "malformed base URL reached HTTP transport" }

func (transport *unexpectedRequestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++

	return nil, unexpectedRequestError{}
}

func newMalformedBaseClient() (*rest.Client, *unexpectedRequestTransport) {
	transport := &unexpectedRequestTransport{calls: 0}
	client := rest.NewClient(
		rest.WithBaseURI("http://%gh"),
		rest.WithHTTPClient(&http.Client{Transport: transport}),
	)

	return client, transport
}

func assertMalformedBuilderErrors(t *testing.T, cases []requestBuildCase) {
	t.Helper()

	client, transport := newMalformedBaseClient()
	ctx := context.Background()

	for _, testCase := range cases {
		err := testCase.call(client, ctx)
		if !ringapimodels.IsNetworkError(err) {
			t.Errorf("%s error = %v, want typed network error", testCase.name, err)
		}

		if transport.calls != 0 {
			t.Errorf("%s reached HTTP transport %d time(s)", testCase.name, transport.calls)
		}
	}
}

func TestCapturedAndDeviceBuildersRejectMalformedBaseURL(t *testing.T) {
	t.Parallel()

	assertMalformedBuilderErrors(t, []requestBuildCase{
		{
			name: "GetDeviceDetail",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetDeviceDetail(ctx, 1)

				return err
			},
		},
		{
			name: "ListLocations",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.ListLocations(ctx)

				return err
			},
		},
		{
			name: "GetLocation",
			call: func(c *rest.Client, ctx context.Context) error {
				var params generatedhttp.GetLocationParams
				_, err := c.GetLocation(ctx, "1", params)

				return err
			},
		},
		{
			name: "ListLocationGroups",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.ListLocationGroups(ctx, "1")

				return err
			},
		},
		{
			name: "ListLocationDevices",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.ListLocationDevices(ctx, "1")

				return err
			},
		},
		{
			name: "GetDeviceTimeline",
			call: func(c *rest.Client, ctx context.Context) error {
				var params generatedhttp.GetDeviceTimelineParams
				_, err := c.GetDeviceTimeline(ctx, 1, params)

				return err
			},
		},
		{
			name: "GetHistoryDevices",
			call: func(c *rest.Client, ctx context.Context) error {
				var params generatedhttp.GetHistoryDevicesParams
				_, err := c.GetHistoryDevices(ctx, params)

				return err
			},
		},
		{
			name: "GetDevices",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetDevices(ctx)

				return err
			},
		},
		{name: "RegisterSession", call: (*rest.Client).RegisterSession},
		{
			name: "GetDeviceHealth",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetDeviceHealth(ctx, 1)

				return err
			},
		},
		{
			name: "RebootDevice",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.RebootDevice(ctx, 1)
			},
		},
		{
			name: "UnlockIntercom",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.UnlockIntercom(ctx, 1)
			},
		},
		{
			name: "SetPersistentLiveViewEnabled",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetPersistentLiveViewEnabled(ctx, 1, true)
			},
		},
		{
			name: "FavoriteRecording",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.FavoriteRecording(ctx, 1)
			},
		},
		{
			name: "DeleteRecording",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.DeleteRecording(ctx, 1, nil)
			},
		},
	})
}

func TestControlBuildersRejectMalformedBaseURL(t *testing.T) {
	t.Parallel()

	assertMalformedBuilderErrors(t, []requestBuildCase{
		{
			name: "GetMotionDetectionEnabled",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetMotionDetectionEnabled(ctx, 1)

				return err
			},
		},
		{
			name: "PatchMotionDetectionEnabled",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.PatchMotionDetectionEnabled(ctx, 1, true)
			},
		},
		{
			name: "SetSirenOn",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetSiren(ctx, 1, true)
			},
		},
		{
			name: "SetSirenOff",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetSiren(ctx, 1, false)
			},
		},
		{
			name: "SetChimeVolume",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetChimeVolume(ctx, 1, "test", 1)
			},
		},
		{
			name: "SetDoorbellVolume",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetDoorbellVolume(ctx, 1, "test", 1)
			},
		},
		{
			name: "SetLightsOn",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetLights(ctx, 1, true)
			},
		},
		{
			name: "SetLightsOff",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetLights(ctx, 1, false)
			},
		},
		{
			name: "SetMotionDetection",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetMotionDetection(ctx, 1, true)
			},
		},
		{
			name: "TestSound",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.TestSound(ctx, 1, "test")
			},
		},
		{
			name: "SetInHomeChimeType",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetInHomeChimeType(ctx, 1, "test", 1)
			},
		},
		{
			name: "SetInHomeChimeDuration",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetInHomeChimeDuration(ctx, 1, "test", 1)
			},
		},
		{
			name: "SetInHomeChimeEnabled",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SetInHomeChimeEnabled(ctx, 1, "test", true)
			},
		},
	})
}

func TestPushAndRecordingBuildersRejectMalformedBaseURL(t *testing.T) {
	t.Parallel()

	assertMalformedBuilderErrors(t, []requestBuildCase{
		{
			name: "RegisterPushDevice",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.RegisterPushDevice(ctx, "token")
			},
		},
		{
			name: "SubscribeDeviceDing",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SubscribeDeviceDing(ctx, 1)
			},
		},
		{
			name: "SubscribeDeviceMotion",
			call: func(c *rest.Client, ctx context.Context) error {
				return c.SubscribeDeviceMotion(ctx, 1)
			},
		},
		{
			name: "GetRecordingShareURL",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetRecordingShareURL(ctx, 1)

				return err
			},
		},
		{
			name: "GetDeviceHistory",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetDeviceHistory(ctx, 1, 1, "motion", nil)

				return err
			},
		},
		{
			name: "GetActiveDings",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetActiveDings(ctx)

				return err
			},
		},
		{
			name: "GetRecording",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.GetRecording(ctx, 1)

				return err
			},
		},
	})
}

func TestSnapshotBuildersRejectMalformedBaseURL(t *testing.T) {
	t.Parallel()

	assertMalformedBuilderErrors(t, []requestBuildCase{
		{
			name: "RefreshSnapshotTimestamp",
			call: func(c *rest.Client, ctx context.Context) error {
				_, err := c.RefreshSnapshotTimestamp(ctx, 1)

				return err
			},
		},
		{
			name: "GetSnapshotImage",
			call: func(c *rest.Client, ctx context.Context) error {
				_, _, err := c.GetSnapshotImage(ctx, 1)

				return err
			},
		},
	})
}
