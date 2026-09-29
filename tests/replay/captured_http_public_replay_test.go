package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"github.com/stretchr/testify/require"
)

func capturedExchange(t *testing.T, name string) replay.Exchange {
	t.Helper()

	path := filepath.Join("fixtures", "http", "historical", name+".json")
	if strings.HasSuffix(name, "-02") || strings.HasSuffix(name, "-03") {
		path = filepath.Join("fixtures", "http", "historical", "variants", name+".json")
	}

	exchange, err := replay.LoadExchange(path)
	require.NoError(t, err)

	exchange.Request.Path = strings.ReplaceAll(exchange.Request.Path, "{device_id}", "1000")
	exchange.Request.Path = strings.ReplaceAll(exchange.Request.Path, "{recording_id}", "1000")
	exchange.Request.Path = strings.ReplaceAll(exchange.Request.Path, "{location_id}", "location-1")

	return exchange
}

func capturedQueryValue(x replay.Exchange, key string) string {
	for _, pair := range x.Request.Query {
		if pair.Name == key {
			return pair.Value
		}
	}

	return ""
}

func capturedTimelineParams(t *testing.T, exchange replay.Exchange) ring.TimelineParams {
	t.Helper()

	start, err := time.Parse(time.RFC3339, capturedQueryValue(exchange, "start_time"))
	require.NoError(t, err)
	end, err := time.Parse(time.RFC3339, capturedQueryValue(exchange, "end_time"))
	require.NoError(t, err)

	order := ring.TimelineOrder(capturedQueryValue(exchange, "order"))
	capabilities := capturedCapabilities(exchange)
	limit := 20

	return ring.TimelineParams{
		StartTime:    &start,
		EndTime:      &end,
		Order:        &order,
		Limit:        &limit,
		Capabilities: capabilities,
	}
}

func capturedCapabilities(x replay.Exchange) []ring.EventCapability {
	value := capturedQueryValue(x, "capabilities")
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")

	capabilities := make([]ring.EventCapability, len(parts))

	for i, part := range parts {
		capabilities[i] = ring.EventCapability(part)
	}

	return capabilities
}

func capturedLocationExpansions(x replay.Exchange) []ring.LocationExpansion {
	value := capturedQueryValue(x, "include")
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")

	expansions := make([]ring.LocationExpansion, len(parts))

	for i, part := range parts {
		expansions[i] = ring.LocationExpansion(part)
	}

	return expansions
}

func TestCapturedHTTPPublicReads(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		capturedDeviceDetailFixture,
		"device-detail-02",
		"device-detail-03",
		"location-list",
		"location-detail",
		"groups",
		"group-devices",
		capturedDeviceTimelineFixture,
		"device-timeline-02",
		"history-devices",
		"history-devices-02",
		capturedBootstrapTicketFixture,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exchange := capturedExchange(t, name)
			transport := replay.NewTransport(exchange)
			client, err := ring.NewClient(
				ring.WithHTTPClient(&http.Client{Transport: transport}),
				ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}),
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			assertCapturedDeviceAndLocationRead(t, name, client, exchange)
			assertCapturedTimelineAndTicketRead(t, name, client, exchange)

			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func assertCapturedDeviceAndLocationRead(t *testing.T, name string, client ring.ClientAPI, exchange replay.Exchange) {
	t.Helper()

	ctx := context.Background()

	switch name {
	case capturedDeviceDetailFixture, "device-detail-02", "device-detail-03":
		result, callErr := client.GetDeviceDetail(
			ctx,
			ring.GetDeviceDetailRequest{
				Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				DeviceID: "1000",
			},
		)
		require.NoError(t, callErr)
		require.Equal(t, int64(1000), result.Device.ID)

		if name == capturedDeviceDetailFixture {
			require.Equal(t, ring.DeviceKindStickUpMiniPTZ, result.Device.Kind)
			status := result.Device.Status()
			require.Equal(t, ring.ConnectionOnline, *status.Connection)
			require.False(t, status.IsOffline)
			require.Equal(t, ring.PowerModeWired, *status.PowerMode)
			require.Nil(t, status.BatteryPercent)
			require.Equal(t, ring.OwnerID("1000"), *result.Device.Owner.ID)
			require.Contains(t, result.Device.Health.SupportedRPCCommands, "PTZ.Pan.Continuous")
			require.Contains(t, result.OperationSets[",owner"], "device_live_view")
		}
	case "location-list":
		result, callErr := client.ListLocations(
			ctx,
			ring.ListLocationsRequest{Auth: ring.AuthContext{AccessToken: "captured-token", HardwareID: ""}},
		)
		require.NoError(t, callErr)
		require.NotEmpty(t, result.Locations)
		require.Contains(t, result.OperationSets["owner"], "location_add_device")
	case "location-detail":
		result, callErr := client.GetLocation(
			ctx,
			ring.GetLocationRequest{
				Auth:       ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				LocationID: "location-1",
				Params:     ring.LocationParams{Include: capturedLocationExpansions(exchange)},
			},
		)
		require.NoError(t, callErr)
		require.NotEmpty(t, result.Data.ID)
		require.Equal(t, ring.LocationResourceLocations, result.Data.Type)
	case "groups":
		result, callErr := client.ListLocationGroups(
			ctx,
			ring.LocationRequest{
				Auth:       ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				LocationID: "location-1",
			},
		)
		require.NoError(t, callErr)
		require.NotNil(t, result.IsOwner)
	case "group-devices":
		result, callErr := client.ListLocationDevices(
			ctx,
			ring.LocationRequest{
				Auth:       ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				LocationID: "location-1",
			},
		)
		require.NoError(t, callErr)
		require.NotNil(t, result.Groups)
	}
}

func assertCapturedTimelineAndTicketRead(t *testing.T, name string, client ring.ClientAPI, exchange replay.Exchange) {
	t.Helper()

	ctx := context.Background()

	switch name {
	case capturedDeviceTimelineFixture, "device-timeline-02":
		result, callErr := client.GetDeviceTimeline(
			ctx,
			ring.GetDeviceTimelineRequest{
				Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				DeviceID: "1000",
				Params:   capturedTimelineParams(t, exchange),
			},
		)
		require.NoError(t, callErr)

		var recorded generatedhttp.DeviceTimeline

		require.NoError(t, json.Unmarshal(exchange.Response.Body, &recorded))
		require.Len(t, result.Items, len(recorded.Items))

		if name == capturedDeviceTimelineFixture {
			require.Equal(t, ring.TimelineEventOnDemand, result.Items[0].Type)
			require.Equal(t, ring.RecordingStatusReady, *result.Items[0].RecordingStatus)
			require.Equal(t, ring.TimelineStateCompleted, *result.Items[0].State)
		}
	case "history-devices", "history-devices-02":
		sourceIDs := strings.Split(capturedQueryValue(exchange, "source_ids"), ",")
		result, callErr := client.GetHistoryDevices(
			ctx,
			ring.GetHistoryDevicesRequest{
				Auth:   ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				Params: ring.HistoryDevicesParams{SourceIDs: sourceIDs, Capabilities: capturedCapabilities(exchange)},
			},
		)
		require.NoError(t, callErr)
		require.NotEmpty(t, result.Events)
	case capturedBootstrapTicketFixture:
		locationID, subscription, requestedTransport := capturedQueryValue(
			exchange,
			"locationID",
		), capturedQueryValue(
			exchange,
			"locationSubscription",
		), ring.SignalingTransport(
			capturedQueryValue(exchange, "requestedTransport"),
		)
		allow, extended := false, true
		params := ring.CapturedTicketsParams{
			LocationID:                       &locationID,
			LocationSubscription:             &subscription,
			RequestedTransport:               &requestedTransport,
			AllowUserOnly:                    &allow,
			EnableExtendedEmergencyCellUsage: &extended,
		}
		result, callErr := client.GetCapturedTickets(
			ctx,
			ring.GetCapturedTicketsRequest{
				Auth:   ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
				Params: params,
			},
		)
		require.NoError(t, callErr)
		require.NotEmpty(t, result.Ticket)
	}
}

func TestCapturedDeviceStatus(t *testing.T) {
	t.Parallel()

	transport := replay.NewTransport(capturedExchange(t, capturedDeviceDetailFixture))
	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: transport}),
		ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	status, err := client.GetDeviceStatus(
		context.Background(),
		ring.GetDeviceDetailRequest{
			Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
			DeviceID: "1000",
		},
	)
	require.NoError(t, err)
	require.Equal(t, ring.ConnectionOnline, *status.Connection)
	require.Nil(t, status.BatteryPercent)
}

func TestCapturedHTTPPublicMutations(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		capturedDeviceRebootFixture,
		"duos-update",
		"duos-update-02",
		"recording-favorite",
		"recording-delete",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exchange := capturedExchange(t, name)
			transport := replay.NewTransport(exchange)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			ctx := context.Background()

			switch name {
			case capturedDeviceRebootFixture:
				err = client.RebootDevice(
					ctx,
					ring.DeviceIDRequest{
						Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
						DeviceID: "1000",
					},
				)
			case "duos-update", "duos-update-02":
				var body generatedhttp.LiveViewSettingRequest

				require.NoError(t, json.Unmarshal(exchange.Request.Body, &body))
				err = client.SetPersistentLiveViewEnabled(
					ctx,
					ring.SetPersistentLiveViewEnabledRequest{
						Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
						DeviceID: "1000",
						Enabled:  body.Entity.LiveViewEnabled,
					},
				)
			case "recording-favorite":
				err = client.FavoriteRecording(
					ctx,
					ring.RecordingIDRequest{
						Auth:        ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
						RecordingID: 1000,
					},
				)
			case "recording-delete":
				confirm := false
				err = client.DeleteRecording(
					ctx,
					ring.DeleteRecordingRequest{
						Auth:                  ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
						RecordingID:           1000,
						ConfirmDeleteFavorite: &confirm,
					},
				)
			}

			require.NoError(t, err)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedHTTPPublicValidation(t *testing.T) {
	t.Parallel()

	client, err := ring.NewClient()
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.GetDeviceDetail(
		context.Background(),
		ring.GetDeviceDetailRequest{
			Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
			DeviceID: "0",
		},
	)
	require.True(t, ringapimodels.IsBadRequestError(err))
	_, err = client.GetLocation(
		context.Background(),
		ring.GetLocationRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}},
	)
	require.True(t, ringapimodels.IsBadRequestError(err))
	_, err = client.GetDeviceTimeline(
		context.Background(),
		ring.GetDeviceTimelineRequest{
			Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
			DeviceID: "1000",
			Params:   ring.TimelineParams{Limit: chimePointer(0)},
		},
	)
	require.True(t, ringapimodels.IsBadRequestError(err))
	err = client.FavoriteRecording(
		context.Background(),
		ring.RecordingIDRequest{Auth: ring.AuthContext{AccessToken: "captured-token"}},
	)
	require.True(t, ringapimodels.IsBadRequestError(err))
}

func TestCapturedHTTPPublicFailureClasses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		match  func(error) bool
	}{
		{"bad-request", http.StatusBadRequest, ringapimodels.IsBadRequestError},
		{"unauthorized", http.StatusUnauthorized, ringapimodels.IsUnauthorizedError},
		{"forbidden", http.StatusForbidden, ringapimodels.IsUnauthorizedError},
		{"not-found", http.StatusNotFound, ringapimodels.IsNotFoundError},
		{"rate-limit", http.StatusTooManyRequests, ringapimodels.IsRateLimitError},
		{"server-error", http.StatusServiceUnavailable, ringapimodels.IsInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := capturedDeviceDetailFixture
			if tc.status >= http.StatusInternalServerError {
				fixture = capturedDeviceRebootFixture
			}

			x := capturedExchange(t, fixture)
			x.Response.Status = tc.status
			x.Response.Body = json.RawMessage(`{"error":"synthetic failure"}`)
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })

			if fixture == capturedDeviceRebootFixture {
				err = client.RebootDevice(
					context.Background(),
					ring.DeviceIDRequest{
						Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
						DeviceID: "1000",
					},
				)
			} else {
				var result *ring.DeviceDetail

				result, err = client.GetDeviceDetail(
					context.Background(),
					ring.GetDeviceDetailRequest{
						Auth:     ring.AuthContext{AccessToken: "captured-token", HardwareID: ""},
						DeviceID: "1000",
					},
				)
				require.Nil(t, result)
			}

			require.True(t, tc.match(err), "error = %v", err)
			require.True(t, ringapimodels.IsHTTPStatusCode(err, tc.status))
			require.NoError(t, transport.AssertConsumed())
		})
	}
}
