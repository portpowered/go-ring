package replay_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
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
	path := filepath.Join("fixtures", "recordings", "http", name+".json")
	if strings.HasSuffix(name, "-02") || strings.HasSuffix(name, "-03") {
		path = filepath.Join("fixtures", "recordings", "http", "variants", name+".json")
	}
	x, err := replay.LoadExchange(path)
	require.NoError(t, err)
	x.Request.Path = strings.ReplaceAll(x.Request.Path, "{device_id}", "1000")
	x.Request.Path = strings.ReplaceAll(x.Request.Path, "{recording_id}", "1000")
	x.Request.Path = strings.ReplaceAll(x.Request.Path, "{location_id}", "location-1")
	return x
}

func capturedQueryValue(x replay.Exchange, key string) string {
	for _, pair := range x.Request.Query {
		if pair.Name == key {
			return pair.Value
		}
	}
	return ""
}

func capturedTimelineParams(t *testing.T, x replay.Exchange) generatedhttp.GetDeviceTimelineParams {
	t.Helper()
	start, err := time.Parse(time.RFC3339, capturedQueryValue(x, "start_time"))
	require.NoError(t, err)
	end, err := time.Parse(time.RFC3339, capturedQueryValue(x, "end_time"))
	require.NoError(t, err)
	order := capturedQueryValue(x, "order")
	capabilities := capturedQueryValue(x, "capabilities")
	limit := 20
	return generatedhttp.GetDeviceTimelineParams{StartTime: &start, EndTime: &end, Order: &order, Limit: &limit, Capabilities: &capabilities}
}

func TestCapturedHTTPPublicReads(t *testing.T) {
	for _, name := range []string{"device-detail", "device-detail-02", "device-detail-03", "location-list", "location-detail", "groups", "group-devices", "device-timeline", "device-timeline-02", "history-devices", "history-devices-02", "bootstrap-ticket"} {
		t.Run(name, func(t *testing.T) {
			x := capturedExchange(t, name)
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}), ring.WithEndpoints(ring.Endpoints{SolutionsBaseURL: "https://prd-api-us.prd.rings.solutions"}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			ctx := context.Background()
			switch name {
			case "device-detail", "device-detail-02", "device-detail-03":
				result, callErr := client.GetDeviceDetail(ctx, ring.GetDeviceDetailRequest{DeviceID: "1000"})
				require.NoError(t, callErr)
				require.Equal(t, int64(1000), result.Device.Id)
			case "location-list":
				result, callErr := client.ListLocations(ctx)
				require.NoError(t, callErr)
				require.NotEmpty(t, result.UserLocations)
			case "location-detail":
				include := capturedQueryValue(x, "include")
				result, callErr := client.GetLocation(ctx, ring.GetLocationRequest{LocationID: "location-1", Params: generatedhttp.GetLocationParams{Include: &include}})
				require.NoError(t, callErr)
				require.NotEmpty(t, result.Data.Id)
			case "groups":
				result, callErr := client.ListLocationGroups(ctx, ring.LocationRequest{LocationID: "location-1"})
				require.NoError(t, callErr)
				require.NotNil(t, result.IsOwner)
			case "group-devices":
				result, callErr := client.ListLocationDevices(ctx, ring.LocationRequest{LocationID: "location-1"})
				require.NoError(t, callErr)
				require.NotNil(t, result.Groups)
			case "device-timeline", "device-timeline-02":
				result, callErr := client.GetDeviceTimeline(ctx, ring.GetDeviceTimelineRequest{DeviceID: "1000", Params: capturedTimelineParams(t, x)})
				require.NoError(t, callErr)
				var recorded generatedhttp.DeviceTimeline
				require.NoError(t, json.Unmarshal(x.Response.Body, &recorded))
				require.Len(t, result.Items, len(recorded.Items))
			case "history-devices", "history-devices-02":
				sourceIDs, capabilities := capturedQueryValue(x, "source_ids"), capturedQueryValue(x, "capabilities")
				result, callErr := client.GetHistoryDevices(ctx, ring.GetHistoryDevicesRequest{Params: generatedhttp.GetHistoryDevicesParams{SourceIds: &sourceIDs, Capabilities: &capabilities}})
				require.NoError(t, callErr)
				require.NotEmpty(t, result.Events)
			case "bootstrap-ticket":
				locationID, subscription, requestedTransport := capturedQueryValue(x, "locationID"), capturedQueryValue(x, "locationSubscription"), capturedQueryValue(x, "requestedTransport")
				allow, extended := false, true
				params := generatedhttp.GetCapturedLocationTicketsParams{LocationID: &locationID, LocationSubscription: &subscription, RequestedTransport: &requestedTransport, AllowUserOnly: &allow, EnableExtendedEmergencyCellUsage: &extended}
				result, callErr := client.GetCapturedTickets(ctx, ring.GetCapturedTicketsRequest{Params: params})
				require.NoError(t, callErr)
				require.NotEmpty(t, result.Ticket)
			}
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedHTTPPublicMutations(t *testing.T) {
	for _, name := range []string{"device-reboot", "duos-update", "duos-update-02", "recording-favorite", "recording-delete"} {
		t.Run(name, func(t *testing.T) {
			x := capturedExchange(t, name)
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			ctx := context.Background()
			switch name {
			case "device-reboot":
				err = client.RebootDevice(ctx, ring.DeviceIDRequest{DeviceID: "1000"})
			case "duos-update", "duos-update-02":
				var body generatedhttp.LiveViewSettingRequest
				require.NoError(t, json.Unmarshal(x.Request.Body, &body))
				err = client.SetPersistentLiveViewEnabled(ctx, ring.SetPersistentLiveViewEnabledRequest{DeviceID: "1000", Enabled: body.Entity.LiveViewEnabled})
			case "recording-favorite":
				err = client.FavoriteRecording(ctx, ring.RecordingIDRequest{RecordingID: 1000})
			case "recording-delete":
				confirm := false
				err = client.DeleteRecording(ctx, ring.DeleteRecordingRequest{RecordingID: 1000, ConfirmDeleteFavorite: &confirm})
			}
			require.NoError(t, err)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestCapturedHTTPPublicValidation(t *testing.T) {
	client, err := ring.NewClient(ring.WithAccessToken("validation-token"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	_, err = client.GetDeviceDetail(context.Background(), ring.GetDeviceDetailRequest{DeviceID: "0"})
	require.True(t, ringapimodels.IsBadRequestError(err))
	_, err = client.GetLocation(context.Background(), ring.GetLocationRequest{})
	require.True(t, ringapimodels.IsBadRequestError(err))
	_, err = client.GetDeviceTimeline(context.Background(), ring.GetDeviceTimelineRequest{DeviceID: "1000", Params: generatedhttp.GetDeviceTimelineParams{Limit: chimePointer(0)}})
	require.True(t, ringapimodels.IsBadRequestError(err))
	err = client.FavoriteRecording(context.Background(), ring.RecordingIDRequest{})
	require.True(t, ringapimodels.IsBadRequestError(err))
}

func TestCapturedHTTPPublicFailureClasses(t *testing.T) {
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
			fixture := "device-detail"
			if tc.status >= http.StatusInternalServerError {
				fixture = "device-reboot"
			}
			x := capturedExchange(t, fixture)
			x.Response.Status = tc.status
			x.Response.Body = json.RawMessage(`{"error":"synthetic failure"}`)
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("captured-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			if fixture == "device-reboot" {
				err = client.RebootDevice(context.Background(), ring.DeviceIDRequest{DeviceID: "1000"})
			} else {
				var result *generatedhttp.DeviceDetail
				result, err = client.GetDeviceDetail(context.Background(), ring.GetDeviceDetailRequest{DeviceID: "1000"})
				require.Nil(t, result)
			}
			require.True(t, tc.match(err), "error = %v", err)
			require.True(t, ringapimodels.IsHTTPStatusCode(err, tc.status))
			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func TestPythonFamilyHealthReplay(t *testing.T) {
	for _, tc := range []struct {
		name, family, id, fixture, firmware string
	}{
		{"doorbot", "doorbots", "987652", "ring_doorboot_health_attrs.json", "1.9.2"},
		{"chime", "chimes", "999999", "ring_chime_health_attrs.json", "1.2.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("fixtures", "legacy", tc.fixture))
			require.NoError(t, err)
			x := replay.Exchange{
				Request:  replay.Request{Method: http.MethodGet, Origin: "https://api.ring.com", Path: "/clients_api/" + tc.family + "/" + tc.id + "/health", Headers: http.Header{"Accept": {"application/json"}}, HeadersMode: replay.HeadersRequired},
				Response: replay.Response{Status: http.StatusOK, Headers: http.Header{"Content-Type": {"application/json"}}, Body: body, JSON: true},
			}
			transport := replay.NewTransport(x)
			client, err := ring.NewClient(ring.WithAccessToken("portable-token"), ring.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = client.Close() })
			health, err := client.UpdateDeviceHealth(context.Background(), ring.UpdateDeviceHealthRequest{DeviceID: tc.id, Family: generatedhttp.DeviceFamilyCode(tc.family)})
			require.NoError(t, err)
			require.Equal(t, tc.firmware, *health.FirmwareVersion)
			require.Equal(t, 100, *health.BatteryLevel)
			require.NotNil(t, health.SignalStrength)
			require.NoError(t, transport.AssertConsumed())
		})
	}
}
