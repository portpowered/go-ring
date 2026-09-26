package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

type capturedPublicCall struct {
	name string
	call func(context.Context, ring.ClientAPI, replay.Exchange) error
}

func capturedPublicCalls() []capturedPublicCall {
	return []capturedPublicCall{
		{"device-detail", func(ctx context.Context, c ring.ClientAPI, _ replay.Exchange) error {
			_, err := c.GetDeviceDetail(ctx, ring.GetDeviceDetailRequest{DeviceID: "1000"})
			return err
		}},
		{"location-list", func(ctx context.Context, c ring.ClientAPI, _ replay.Exchange) error {
			_, err := c.ListLocations(ctx)
			return err
		}},
		{"location-detail", func(ctx context.Context, c ring.ClientAPI, x replay.Exchange) error {
			include := capturedQueryValue(x, "include")
			_, err := c.GetLocation(ctx, ring.GetLocationRequest{LocationID: "location-1", Params: generatedhttp.GetLocationParams{Include: &include}})
			return err
		}},
		{"groups", func(ctx context.Context, c ring.ClientAPI, _ replay.Exchange) error {
			_, err := c.ListLocationGroups(ctx, ring.LocationRequest{LocationID: "location-1"})
			return err
		}},
		{"group-devices", func(ctx context.Context, c ring.ClientAPI, _ replay.Exchange) error {
			_, err := c.ListLocationDevices(ctx, ring.LocationRequest{LocationID: "location-1"})
			return err
		}},
		{"device-timeline", func(ctx context.Context, c ring.ClientAPI, x replay.Exchange) error {
			params, err := capturedTimelineParamsForCall(x)
			if err != nil {
				return err
			}
			_, err = c.GetDeviceTimeline(ctx, ring.GetDeviceTimelineRequest{DeviceID: "1000", Params: params})
			return err
		}},
		{"history-devices", func(ctx context.Context, c ring.ClientAPI, x replay.Exchange) error {
			sourceIDs, capabilities := capturedQueryValue(x, "source_ids"), capturedQueryValue(x, "capabilities")
			_, err := c.GetHistoryDevices(ctx, ring.GetHistoryDevicesRequest{Params: generatedhttp.GetHistoryDevicesParams{SourceIds: &sourceIDs, Capabilities: &capabilities}})
			return err
		}},
		{"device-reboot", func(ctx context.Context, c ring.ClientAPI, _ replay.Exchange) error {
			return c.RebootDevice(ctx, ring.DeviceIDRequest{DeviceID: "1000"})
		}},
		{"duos-update", func(ctx context.Context, c ring.ClientAPI, x replay.Exchange) error {
			var body generatedhttp.LiveViewSettingRequest
			if err := json.Unmarshal(x.Request.Body, &body); err != nil {
				return err
			}
			return c.SetPersistentLiveViewEnabled(ctx, ring.SetPersistentLiveViewEnabledRequest{DeviceID: "1000", Enabled: body.Entity.LiveViewEnabled})
		}},
		{"recording-favorite", func(ctx context.Context, c ring.ClientAPI, _ replay.Exchange) error {
			return c.FavoriteRecording(ctx, ring.RecordingIDRequest{RecordingID: 1000})
		}},
		{"recording-delete", func(ctx context.Context, c ring.ClientAPI, _ replay.Exchange) error {
			confirm := false
			return c.DeleteRecording(ctx, ring.DeleteRecordingRequest{RecordingID: 1000, ConfirmDeleteFavorite: &confirm})
		}},
		{"bootstrap-ticket", func(ctx context.Context, c ring.ClientAPI, x replay.Exchange) error {
			locationID, subscription, transport := capturedQueryValue(x, "locationID"), capturedQueryValue(x, "locationSubscription"), capturedQueryValue(x, "requestedTransport")
			allow, extended := false, true
			_, err := c.GetCapturedTickets(ctx, ring.GetCapturedTicketsRequest{Params: generatedhttp.GetCapturedLocationTicketsParams{LocationID: &locationID, LocationSubscription: &subscription, RequestedTransport: &transport, AllowUserOnly: &allow, EnableExtendedEmergencyCellUsage: &extended}})
			return err
		}},
	}
}

func capturedTimelineParamsForCall(x replay.Exchange) (generatedhttp.GetDeviceTimelineParams, error) {
	start, err := time.Parse(time.RFC3339, capturedQueryValue(x, "start_time"))
	if err != nil {
		return generatedhttp.GetDeviceTimelineParams{}, err
	}
	end, err := time.Parse(time.RFC3339, capturedQueryValue(x, "end_time"))
	if err != nil {
		return generatedhttp.GetDeviceTimelineParams{}, err
	}
	order, capabilities, limit := capturedQueryValue(x, "order"), capturedQueryValue(x, "capabilities"), 20
	return generatedhttp.GetDeviceTimelineParams{StartTime: &start, EndTime: &end, Order: &order, Capabilities: &capabilities, Limit: &limit}, nil
}

func TestCapturedPublicHTTPNotFoundForEveryOperation(t *testing.T) {
	for _, tc := range capturedPublicCalls() {
		t.Run(tc.name, func(t *testing.T) {
			x := capturedExchange(t, tc.name)
			x.Response.Status = http.StatusNotFound
			x.Response.Body = json.RawMessage(`{"error":"missing"}`)
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			err = tc.call(context.Background(), client, x)
			require.True(t, ringapimodels.IsNotFoundError(err), "error = %v", err)
			require.True(t, ringapimodels.IsHTTPStatusCode(err, http.StatusNotFound))
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedPublicHTTPValidationNeverSends(t *testing.T) {
	transport := replay.NewTransport()
	client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"detail", func() error {
			_, err := client.GetDeviceDetail(context.Background(), ring.GetDeviceDetailRequest{})
			return err
		}},
		{"location", func() error {
			_, err := client.GetLocation(context.Background(), ring.GetLocationRequest{})
			return err
		}},
		{"groups", func() error {
			_, err := client.ListLocationGroups(context.Background(), ring.LocationRequest{})
			return err
		}},
		{"group-devices", func() error {
			_, err := client.ListLocationDevices(context.Background(), ring.LocationRequest{})
			return err
		}},
		{"timeline-device", func() error {
			_, err := client.GetDeviceTimeline(context.Background(), ring.GetDeviceTimelineRequest{})
			return err
		}},
		{"timeline-limit", func() error {
			_, err := client.GetDeviceTimeline(context.Background(), ring.GetDeviceTimelineRequest{DeviceID: "1000", Params: generatedhttp.GetDeviceTimelineParams{Limit: chimePointer(-1)}})
			return err
		}},
		{"reboot", func() error { return client.RebootDevice(context.Background(), ring.DeviceIDRequest{}) }},
		{"live-view-setting", func() error {
			return client.SetPersistentLiveViewEnabled(context.Background(), ring.SetPersistentLiveViewEnabledRequest{})
		}},
		{"favorite", func() error { return client.FavoriteRecording(context.Background(), ring.RecordingIDRequest{}) }},
		{"delete", func() error { return client.DeleteRecording(context.Background(), ring.DeleteRecordingRequest{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			require.True(t, ringapimodels.IsBadRequestError(err), "error = %v", err)
		})
	}
	require.NoError(t, transport.AssertConsumed())
}

func TestCapturedTicketReplayFailureBoundaries(t *testing.T) {
	fixture := capturedExchange(t, "bootstrap-ticket")
	var call func(context.Context, ring.ClientAPI, replay.Exchange) error
	for _, operation := range capturedPublicCalls() {
		if operation.name == "bootstrap-ticket" {
			call = operation.call
			break
		}
	}
	require.NotNil(t, call)
	t.Run("missing token", func(t *testing.T) {
		transport := replay.NewTransport()
		client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })
		err = call(context.Background(), client, fixture)
		require.True(t, ringapimodels.IsTokenError(err), "error = %v", err)
		require.NoError(t, transport.AssertConsumed())
	})
	for _, tc := range []struct {
		name string
		body json.RawMessage
	}{
		{"malformed-json", json.RawMessage(`{`)},
		{"missing-ticket", json.RawMessage(`{}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := fixture
			x.Response.Body = tc.body
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			err = call(context.Background(), client, x)
			require.True(t, ringapimodels.IsInternalServerError(err), "error = %v", err)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedHTTPRetryReadButNeverReboot(t *testing.T) {
	t.Run("device detail retries transient server failure", func(t *testing.T) {
		failure := capturedExchange(t, "device-detail")
		failure.Response.Status = http.StatusServiceUnavailable
		failure.Response.Body = json.RawMessage(`{"error":"temporarily unavailable"}`)
		success := capturedExchange(t, "device-detail")
		transport := replay.NewTransport(failure, success)
		client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })
		detail, err := client.GetDeviceDetail(context.Background(), ring.GetDeviceDetailRequest{DeviceID: "1000"})
		require.NoError(t, err)
		require.Equal(t, int64(1000), detail.Device.Id)
		require.NoError(t, transport.AssertConsumed())
	})
	t.Run("reboot is not retried", func(t *testing.T) {
		failure := capturedExchange(t, "device-reboot")
		failure.Response.Status = http.StatusServiceUnavailable
		failure.Response.Body = json.RawMessage(`{"error":"temporarily unavailable"}`)
		transport := replay.NewTransport(failure)
		client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })
		err = client.RebootDevice(context.Background(), ring.DeviceIDRequest{DeviceID: "1000"})
		require.True(t, ringapimodels.IsInternalServerError(err), "error = %v", err)
		require.True(t, ringapimodels.IsHTTPStatusCode(err, http.StatusServiceUnavailable))
		require.NoError(t, transport.AssertConsumed())
	})
}
