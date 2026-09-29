package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
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
	return append(capturedLocationCalls(), capturedControlCalls()...)
}

func capturedLocationCalls() []capturedPublicCall {
	return []capturedPublicCall{
		{capturedDeviceDetailFixture, func(ctx context.Context, client ring.ClientAPI, _ replay.Exchange) error {
			_, err := client.GetDeviceDetail(
				ctx,
				ring.GetDeviceDetailRequest{
					Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					DeviceID: "1000",
				},
			)

			return wrapReplayTestError("get captured device detail", err)
		}},
		{"location-list", func(ctx context.Context, client ring.ClientAPI, _ replay.Exchange) error {
			_, err := client.ListLocations(
				ctx,
				ring.ListLocationsRequest{Auth: ring.AuthContext{AccessToken: "captured-token", HardwareID: ""}},
			)

			return wrapReplayTestError("list captured locations", err)
		}},
		{"location-detail", func(ctx context.Context, client ring.ClientAPI, exchange replay.Exchange) error {
			_, err := client.GetLocation(
				ctx,
				ring.GetLocationRequest{
					Auth:       ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					LocationID: "location-1",
					Params:     ring.LocationParams{Include: capturedLocationExpansions(exchange)},
				},
			)

			return wrapReplayTestError("get captured location", err)
		}},
		{"groups", func(ctx context.Context, client ring.ClientAPI, _ replay.Exchange) error {
			_, err := client.ListLocationGroups(
				ctx,
				ring.LocationRequest{
					Auth:       ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					LocationID: "location-1",
				},
			)

			return wrapReplayTestError("list captured location groups", err)
		}},
		{"group-devices", func(ctx context.Context, client ring.ClientAPI, _ replay.Exchange) error {
			_, err := client.ListLocationDevices(
				ctx,
				ring.LocationRequest{
					Auth:       ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					LocationID: "location-1",
				},
			)

			return wrapReplayTestError("list captured location devices", err)
		}},
		{capturedDeviceTimelineFixture, func(ctx context.Context, client ring.ClientAPI, exchange replay.Exchange) error {
			params, err := capturedTimelineParamsForCall(exchange)
			if err != nil {
				return err
			}

			_, err = client.GetDeviceTimeline(
				ctx,
				ring.GetDeviceTimelineRequest{
					Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					DeviceID: "1000",
					Params:   params,
				},
			)

			return wrapReplayTestError("get captured device timeline", err)
		}},
	}
}

func capturedControlCalls() []capturedPublicCall {
	return []capturedPublicCall{
		{"history-devices", func(ctx context.Context, client ring.ClientAPI, exchange replay.Exchange) error {
			sourceIDs := strings.Split(capturedQueryValue(exchange, "source_ids"), ",")
			_, err := client.GetHistoryDevices(
				ctx,
				ring.GetHistoryDevicesRequest{
					Auth:   ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					Params: ring.HistoryDevicesParams{SourceIDs: sourceIDs, Capabilities: capturedCapabilities(exchange)},
				},
			)

			return wrapReplayTestError("get captured history devices", err)
		}},
		{capturedDeviceRebootFixture, func(ctx context.Context, client ring.ClientAPI, _ replay.Exchange) error {
			return client.RebootDevice(
				ctx,
				ring.DeviceIDRequest{
					Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					DeviceID: "1000",
				},
			)
		}},
		{"duos-update", func(ctx context.Context, client ring.ClientAPI, exchange replay.Exchange) error {
			var body generatedhttp.LiveViewSettingRequest

			err := json.Unmarshal(exchange.Request.Body, &body)
			if err != nil {
				return wrapReplayTestError("decode captured setting request", err)
			}

			err = client.SetPersistentLiveViewEnabled(
				ctx,
				ring.SetPersistentLiveViewEnabledRequest{
					Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					DeviceID: "1000",
					Enabled:  body.Entity.LiveViewEnabled,
				},
			)

			return wrapReplayTestError("set captured persistent live view", err)
		}},
		{"recording-favorite", func(ctx context.Context, client ring.ClientAPI, _ replay.Exchange) error {
			return client.FavoriteRecording(
				ctx,
				ring.RecordingIDRequest{
					Auth:        ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					RecordingID: 1000,
				},
			)
		}},
		{"recording-delete", func(ctx context.Context, client ring.ClientAPI, _ replay.Exchange) error {
			confirm := false

			return client.DeleteRecording(
				ctx,
				ring.DeleteRecordingRequest{
					Auth:                  ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					RecordingID:           1000,
					ConfirmDeleteFavorite: &confirm,
				},
			)
		}},
		{capturedBootstrapTicketFixture, func(ctx context.Context, client ring.ClientAPI, exchange replay.Exchange) error {
			locationID, subscription, transport := capturedQueryValue(
				exchange,
				"locationID",
			), capturedQueryValue(
				exchange,
				"locationSubscription",
			), ring.SignalingTransport(
				capturedQueryValue(exchange, "requestedTransport"),
			)
			allow, extended := false, true
			_, err := client.GetCapturedTickets(
				ctx,
				ring.GetCapturedTicketsRequest{
					Auth: ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
					Params: ring.CapturedTicketsParams{
						LocationID:                       &locationID,
						LocationSubscription:             &subscription,
						RequestedTransport:               &transport,
						AllowUserOnly:                    &allow,
						EnableExtendedEmergencyCellUsage: &extended,
					},
				},
			)

			return wrapReplayTestError("get captured signaling tickets", err)
		}},
	}
}

func capturedTimelineParamsForCall(exchange replay.Exchange) (ring.TimelineParams, error) {
	start, err := time.Parse(time.RFC3339, capturedQueryValue(exchange, "start_time"))
	if err != nil {
		return ring.TimelineParams{}, wrapReplayTestError("parse captured timeline start time", err)
	}

	end, err := time.Parse(time.RFC3339, capturedQueryValue(exchange, "end_time"))
	if err != nil {
		return ring.TimelineParams{}, wrapReplayTestError("parse captured timeline end time", err)
	}

	order, limit := ring.TimelineOrder(capturedQueryValue(exchange, "order")), 20

	return ring.TimelineParams{
		StartTime:    &start,
		EndTime:      &end,
		Order:        &order,
		Capabilities: capturedCapabilities(exchange),
		Limit:        &limit,
	}, nil
}

func TestCapturedPublicHTTPNotFoundForEveryOperation(t *testing.T) {
	t.Parallel()

	for _, tc := range capturedPublicCalls() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exchange := capturedExchange(t, tc.name)
			exchange.Response.Status = http.StatusNotFound
			exchange.Response.Body = json.RawMessage(`{"error":"missing"}`)
			transport := replay.NewTransport(exchange)
			client, err := ring.NewClient(
				ring.WithHTTPClient(&http.Client{Transport: transport}),
				ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			err = tc.call(context.Background(), client, exchange)
			require.True(t, ringapimodels.IsNotFoundError(err), "error = %v", err)
			require.True(t, ringapimodels.IsHTTPStatusCode(err, http.StatusNotFound))
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedPublicHTTPValidationNeverSends(t *testing.T) {
	t.Parallel()

	auth := ring.AuthContext{AccessToken: "captured-token", HardwareID: ""}

	for _, tc := range []struct {
		name string
		call func(ring.ClientAPI) error
	}{
		{"detail", func(client ring.ClientAPI) error {
			_, err := client.GetDeviceDetail(
				context.Background(),
				ring.GetDeviceDetailRequest{Auth: auth},
			)

			return wrapReplayTestError("validate captured device detail", err)
		}},
		{"location", func(client ring.ClientAPI) error {
			_, err := client.GetLocation(
				context.Background(),
				ring.GetLocationRequest{Auth: auth},
			)

			return wrapReplayTestError("validate captured location detail", err)
		}},
		{"groups", func(client ring.ClientAPI) error {
			_, err := client.ListLocationGroups(
				context.Background(),
				ring.LocationRequest{Auth: auth},
			)

			return wrapReplayTestError("validate captured location groups", err)
		}},
		{"group-devices", func(client ring.ClientAPI) error {
			_, err := client.ListLocationDevices(
				context.Background(),
				ring.LocationRequest{Auth: auth},
			)

			return wrapReplayTestError("validate captured location devices", err)
		}},
		{"timeline-device", func(client ring.ClientAPI) error {
			_, err := client.GetDeviceTimeline(
				context.Background(),
				ring.GetDeviceTimelineRequest{Auth: auth},
			)

			return wrapReplayTestError("validate captured device timeline", err)
		}},
		{"timeline-limit", func(client ring.ClientAPI) error {
			_, err := client.GetDeviceTimeline(
				context.Background(),
				ring.GetDeviceTimelineRequest{
					Auth:     auth,
					DeviceID: "1000",
					Params:   ring.TimelineParams{Limit: chimePointer(-1)},
				},
			)

			return wrapReplayTestError("validate captured device timeline limit", err)
		}},
		{"reboot", func(client ring.ClientAPI) error {
			return client.RebootDevice(
				context.Background(),
				ring.DeviceIDRequest{Auth: auth},
			)
		}},
		{"live-view-setting", func(client ring.ClientAPI) error {
			return client.SetPersistentLiveViewEnabled(
				context.Background(),
				ring.SetPersistentLiveViewEnabledRequest{Auth: auth},
			)
		}},
		{"favorite", func(client ring.ClientAPI) error {
			return client.FavoriteRecording(
				context.Background(),
				ring.RecordingIDRequest{Auth: auth},
			)
		}},
		{"delete", func(client ring.ClientAPI) error {
			return client.DeleteRecording(
				context.Background(),
				ring.DeleteRecordingRequest{Auth: auth},
			)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			transport := replay.NewTransport()
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			err = tc.call(client)
			require.True(t, ringapimodels.IsBadRequestError(err), "error = %v", err)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedTicketReplayFailureBoundaries(t *testing.T) {
	t.Parallel()

	fixture := capturedExchange(t, capturedBootstrapTicketFixture)

	var call func(context.Context, ring.ClientAPI, replay.Exchange) error

	for _, operation := range capturedPublicCalls() {
		if operation.name == capturedBootstrapTicketFixture {
			call = operation.call

			break
		}
	}

	require.NotNil(t, call)
	t.Run("missing token", func(t *testing.T) {
		t.Parallel()

		transport := replay.NewTransport()
		client, err := ring.NewClient(
			ring.WithHTTPClient(&http.Client{Transport: transport}),
			ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })

		locationID, subscription, requestedTransport := capturedQueryValue(
			fixture,
			"locationID",
		), capturedQueryValue(
			fixture,
			"locationSubscription",
		), ring.SignalingTransport(
			capturedQueryValue(fixture, "requestedTransport"),
		)
		allow, extended := false, true
		_, err = client.GetCapturedTickets(
			context.Background(),
			ring.GetCapturedTicketsRequest{
				Params: ring.CapturedTicketsParams{
					LocationID:                       &locationID,
					LocationSubscription:             &subscription,
					RequestedTransport:               &requestedTransport,
					AllowUserOnly:                    &allow,
					EnableExtendedEmergencyCellUsage: &extended,
				},
			},
		)
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
			t.Parallel()

			exchange := fixture
			exchange.Response.Body = tc.body
			transport := replay.NewTransport(exchange)
			client, err := ring.NewClient(
				ring.WithHTTPClient(&http.Client{Transport: transport}),
				ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			err = call(context.Background(), client, exchange)
			require.True(t, ringapimodels.IsInternalServerError(err), "error = %v", err)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedHTTPRetryReadButNeverReboot(t *testing.T) {
	t.Parallel()

	t.Run("device detail retries transient server failure", func(t *testing.T) {
		t.Parallel()

		failure := capturedExchange(t, capturedDeviceDetailFixture)
		failure.Response.Status = http.StatusServiceUnavailable
		failure.Response.Body = json.RawMessage(`{"error":"temporarily unavailable"}`)
		success := capturedExchange(t, capturedDeviceDetailFixture)
		transport := replay.NewTransport(failure, success)
		client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })

		detail, err := client.GetDeviceDetail(
			context.Background(),
			ring.GetDeviceDetailRequest{
				Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				DeviceID: "1000",
			},
		)
		require.NoError(t, err)
		require.Equal(t, int64(1000), detail.Device.ID)
		require.NoError(t, transport.AssertConsumed())
	})
	t.Run("reboot is not retried", func(t *testing.T) {
		t.Parallel()

		failure := capturedExchange(t, capturedDeviceRebootFixture)
		failure.Response.Status = http.StatusServiceUnavailable
		failure.Response.Body = json.RawMessage(`{"error":"temporarily unavailable"}`)
		transport := replay.NewTransport(failure)
		client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })

		err = client.RebootDevice(
			context.Background(),
			ring.DeviceIDRequest{
				Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				DeviceID: "1000",
			},
		)
		require.True(t, ringapimodels.IsInternalServerError(err), "error = %v", err)
		require.True(t, ringapimodels.IsHTTPStatusCode(err, http.StatusServiceUnavailable))
		require.NoError(t, transport.AssertConsumed())
	})
}
