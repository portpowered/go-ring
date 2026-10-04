package replay_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
	"github.com/stretchr/testify/require"
)

type syntheticOperationPair struct {
	replay.Exchange

	OperationID string `json:"operation_id"`
}

type pairedRoute struct {
	Method string `json:"method"`
	Origin string `json:"origin"`
	Path   string `json:"path"`
}

type pairedRouteEnvelope struct {
	Request pairedRoute `json:"request"`
}

func loadMissingOperationPairs(t *testing.T) []syntheticOperationPair {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("fixtures", "http", "synthetic", "paired", "missing-operations.json"))
	require.NoError(t, err)

	var pairs []syntheticOperationPair

	require.NoError(t, json.Unmarshal(data, &pairs))
	require.Len(t, pairs, 17)

	return pairs
}

// The inventory is derived from the checked-in OpenAPI document and complete
// stored pairs, so adding a supported operation without a pair fails CI.
func TestCompletePairedOperationInventory(t *testing.T) {
	t.Parallel()

	doc := loadYAML(t, filepath.Join("..", "..", "api", "openapi.yaml"))
	paths := object(doc["paths"])
	covered := collectStoredPairCoverage(t, paths)

	var missing []string

	for _, rawPath := range paths {
		for _, rawOperation := range object(rawPath) {
			if id, ok := object(rawOperation)["operationId"].(string); ok && !covered[id] {
				missing = append(missing, id)
			}
		}
	}

	require.Empty(t, missing, "OpenAPI operations without a complete stored pair")
	require.Len(t, covered, 39)
}

func collectStoredPairCoverage(t *testing.T, paths map[string]any) map[string]bool {
	t.Helper()

	covered := make(map[string]bool)

	for _, root := range []string{
		filepath.Join("fixtures", "http", "synthetic"),
		filepath.Join("fixtures", "auth", "synthetic"),
	} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".json" {
				return walkErr
			}

			return collectCoveredFromFixture(path, paths, covered)
		})
		require.NoError(t, err)
	}

	return covered
}

func collectCoveredFromFixture(path string, paths map[string]any, covered map[string]bool) error {
	data, readErr := os.ReadFile(path) // #nosec G304 -- path is from the fixed repository fixture tree.
	if readErr != nil {
		return wrapReplayTestError("read paired synthetic fixture "+path, readErr)
	}

	for _, raw := range pairObjects(data) {
		var pair pairedRouteEnvelope
		if json.Unmarshal(raw, &pair) != nil || pair.Request.Method == "" || pair.Request.Origin == "" ||
			pair.Request.Path == "" {
			continue
		}

		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || !completeStoredPair(fields) {
			continue
		}

		operation := object(templatePath(pair.Request.Path, paths)[strings.ToLower(pair.Request.Method)])
		if id, ok := operation["operationId"].(string); ok {
			covered[id] = true
		}
	}

	return nil
}

func TestSyntheticPairMetadataMatchesOpenAPI(t *testing.T) {
	t.Parallel()

	doc := loadYAML(t, filepath.Join("..", "..", "api", "openapi.yaml"))
	paths := object(doc["paths"])

	seen := map[string]bool{}

	for _, name := range []string{"missing-operations.json", "additional-operations.json"} {
		data, err := os.ReadFile(
			filepath.Join("fixtures", "http", "synthetic", "paired", name),
		) // #nosec G304 -- name comes from the fixed synthetic fixture list.
		require.NoError(t, err)

		var pairs []syntheticOperationPair

		require.NoError(t, json.Unmarshal(data, &pairs))

		for _, pair := range pairs {
			require.False(t, seen[pair.OperationID], "duplicate synthetic operation pair")
			seen[pair.OperationID] = true

			require.NotEmpty(t, pair.Request.Origin)
			require.NotNil(t, pair.Request.Query)
			require.NotNil(t, pair.Request.Headers)
			require.NotNil(t, pair.Response.Headers)
			require.Positive(t, pair.Response.Status)
			operation := object(templatePath(pair.Request.Path, paths)[strings.ToLower(pair.Request.Method)])
			require.Equal(t, pair.OperationID, operation["operationId"])
			require.Contains(t, object(operation["responses"]), strconv.Itoa(pair.Response.Status))

			servers, ok := operation["servers"].([]any)
			if !ok {
				servers, _ = doc["servers"].([]any)
			}

			allowed := false
			for _, server := range servers {
				allowed = allowed || object(server)["url"] == pair.Request.Origin
			}

			require.True(t, allowed, "fixture origin absent from operation servers")
		}
	}

	require.Len(t, seen, 38)
}

func pairObjects(data []byte) []json.RawMessage {
	var rows []json.RawMessage
	if json.Unmarshal(data, &rows) == nil {
		return rows
	}

	return []json.RawMessage{data}
}

func completeStoredPair(fields map[string]json.RawMessage) bool {
	var request, response map[string]json.RawMessage
	if json.Unmarshal(fields["request"], &request) != nil || json.Unmarshal(fields["response"], &response) != nil {
		return false
	}

	for _, name := range []string{"method", "origin", "path", "query", "headers", "body"} {
		if _, ok := request[name]; !ok {
			return false
		}
	}

	for _, name := range []string{"status", "headers", "body"} {
		if _, ok := response[name]; !ok {
			return false
		}
	}

	return true
}

// Every newly covered operation passes through its generated HTTP call and a
// stored synthetic request/response pair. The transport refuses a response
// until all request fields match, then AssertConsumed checks exhaustion.
func TestMissingOperationsPairedReplay(t *testing.T) {
	t.Parallel()

	//nolint:bodyclose // The subtest closes each returned response after checking its paired exchange.
	calls := missingOperationCalls()

	for _, pair := range loadMissingOperationPairs(t) {
		t.Run(pair.OperationID, func(t *testing.T) {
			t.Parallel()

			call, ok := calls[pair.OperationID]
			require.True(t, ok, "no generated call for fixture")

			transport := replay.NewTransport(pair.Exchange)
			client, err := generatedhttp.NewClient(
				pair.Request.Origin,
				generatedhttp.WithHTTPClient(&http.Client{Transport: transport}),
			)
			require.NoError(t, err)
			response, err := call(client)
			require.NoError(t, err)
			t.Cleanup(func() { _ = response.Body.Close() })
			require.Equal(t, pair.Response.Status, response.StatusCode)
			require.Equal(t, pair.Response.Headers, response.Header)
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)

			if pair.Response.JSON {
				require.True(t, replay.SemanticEqual(pair.Response.Body, body))
			} else {
				var expected string

				require.NoError(t, json.Unmarshal(pair.Response.Body, &expected))
				require.Equal(t, expected, string(body))
			}

			require.NoError(t, transport.AssertConsumed())
		})
	}
}

func missingOperationCalls() map[string]func(*generatedhttp.Client) (*http.Response, error) {
	ctx := context.Background()
	form := "application/x-www-form-urlencoded"
	accept := func(value string) generatedhttp.RequestEditorFn {
		return func(_ context.Context, request *http.Request) error {
			request.Header.Set("Accept", value)

			if request.URL.Host != "oauth.ring.com" {
				request.Header.Set("Authorization", "Bearer synthetic-access")
			}

			return nil
		}
	}
	calls := map[string]func(*generatedhttp.Client) (*http.Response, error){
		"beginOrContinueOAuthAuthorization": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.BeginOrContinueOAuthAuthorization(ctx, nil, accept("text/html"))
		},
		"submitOAuthCredentials": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.SubmitOAuthCredentialsWithBody(
				ctx,
				form,
				strings.NewReader("csrf-token=synthetic-csrf&password=synthetic-password&username=synthetic-user"),
				accept("application/json"),
			)
		},
		"verifyOAuthTwoFactorCode": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.VerifyOAuthTwoFactorCodeWithBody(
				ctx,
				form,
				strings.NewReader("2fa_code=123456&csrf-token=synthetic-csrf&remember_me=false"),
				accept("application/json"),
			)
		},
		"exchangeOrRefreshOAuthToken": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.ExchangeOrRefreshOAuthTokenWithBody(
				ctx,
				form,
				strings.NewReader(
					"client_id=synthetic-client&code=synthetic-code&"+
						"code_verifier=synthetic-verifier&grant_type=authorization_code&"+
						"redirect_uri=https%3A%2F%2Fsynthetic.example%2Fcallback",
				),
				accept("application/json"),
			)
		},
		"registerClientSession": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.RegisterClientSessionWithBody(
				ctx,
				"application/json",
				strings.NewReader(
					`{"device":{"hardware_id":"synthetic-hard`+
						`ware","metadata":{"api_version":11,"devi`+
						`ce_model":"synthetic-model"},"os":"andro`+
						`id"}}`,
				),
				accept("application/json"),
			)
		},
		"turnFloodlightOff": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.TurnFloodlightOff(ctx, 1000, accept("application/json"))
		},
		"getLegacyDeviceHealth": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetLegacyDeviceHealth(ctx, 1000, accept("application/json"))
		},
		"getLegacyDeviceHistory": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetLegacyDeviceHistory(ctx, 1000, nil, accept("application/json"))
		},
		"getActiveDings": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetActiveDings(ctx, accept("application/json"))
		},
		"streamRecording": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.StreamRecording(ctx, 1000, accept("video/mp4"))
		},
		"getLegacyRecordingShareURL": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetLegacyRecordingShareURL(ctx, 1000, accept("application/json"))
		},
		"refreshLegacySnapshotTimestamp": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.RefreshLegacySnapshotTimestampWithBody(
				ctx,
				"application/json",
				strings.NewReader(`{"doorbot_ids":[1000]}`),
				accept("application/json"),
			)
		},
		"getLegacySnapshotImage": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetLegacySnapshotImage(ctx, 1000, accept("image/jpeg"))
		},
		"setChimeVolume": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.SetChimeVolume(
				ctx,
				1000,
				&generatedhttp.SetChimeVolumeParams{ChimeDescription: "synthetic chime", ChimeSettingsVolume: 5},
				accept("application/json"),
			)
		},
		"updateLegacyDoorbotControls": func(c *generatedhttp.Client) (*http.Response, error) {
			var params generatedhttp.UpdateLegacyDoorbotControlsParams
			params.DoorbotDescription = "synthetic doorbell"

			return c.UpdateLegacyDoorbotControls(
				ctx,
				1000,
				&params,
				accept("application/json"),
			)
		},
		"testChimeSound": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.TestChimeSound(
				ctx,
				1000,
				&generatedhttp.TestChimeSoundParams{Kind: "ding"},
				accept("application/json"),
			)
		},
		"turnFloodlightOn": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.TurnFloodlightOn(ctx, 1000, accept("application/json"))
		},
	}

	return calls
}

func additionalOperationCalls() map[string]func(*generatedhttp.Client) (*http.Response, error) {
	ctx := context.Background()
	accept := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer synthetic-access")

		return nil
	}
	jsonBody := "application/json"
	calls := map[string]func(*generatedhttp.Client) (*http.Response, error){
		"listDevices": func(c *generatedhttp.Client) (*http.Response, error) { return c.ListDevices(ctx, accept) },
		"getDevice":   func(c *generatedhttp.Client) (*http.Response, error) { return c.GetDevice(ctx, 1000, accept) },
		"getDeviceSettings": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetDeviceSettings(ctx, 1000, accept)
		},
		"patchDeviceSettings": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.PatchDeviceSettingsWithBody(
				ctx,
				1000,
				jsonBody,
				strings.NewReader(`{"motion_settings":{"motion_detection_enabled":false}}`),
				accept,
			)
		},
		"getDeviceTimeline": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetDeviceTimeline(ctx, 1000, nil, accept)
		},
		"getHistoryDevices": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetHistoryDevices(ctx, nil, accept)
		},
		"turnSirenOn": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.TurnSirenOn(ctx, 1000, accept)
		},
		"turnSirenOff": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.TurnSirenOff(ctx, 1000, accept)
		},
		"getCapturedLocationTickets": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetCapturedLocationTickets(ctx, nil, accept)
		},
		"sendDeviceCommand": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.SendDeviceCommandWithBody(
				ctx,
				1000,
				jsonBody,
				strings.NewReader(`{"command_name":"reboot"}`),
				accept,
			)
		},
		"unlockIntercom": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.UnlockIntercomWithBody(
				ctx,
				1000,
				jsonBody,
				strings.NewReader(
					`{"command_name":"device_rpc","request":{`+
						`"jsonrpc":"2.0","method":"unlock_door","`+
						`params":{"door_id":0,"user_id":0}}}`,
				),
				accept,
			)
		},
		"setLiveViewEnabled": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.SetLiveViewEnabledWithBody(
				ctx,
				1000,
				jsonBody,
				strings.NewReader(`{"entity":{"live_view_enabled":true}}`),
				accept,
			)
		},
		"listLocationDevices": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.ListLocationDevices(ctx, "synthetic-location", accept)
		},
		"listLocationGroups": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.ListLocationGroups(ctx, "synthetic-location", accept)
		},
		"listLocations": func(c *generatedhttp.Client) (*http.Response, error) { return c.ListLocations(ctx, accept) },
		"getLocation": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.GetLocation(ctx, "synthetic-location", nil, accept)
		},
		"deleteRecording": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.DeleteRecording(ctx, 1000, nil, accept)
		},
		"favoriteRecording": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.FavoriteRecording(ctx, 1000, accept)
		},
		"registerPushDevice": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.RegisterPushDeviceWithBody(
				ctx,
				jsonBody,
				strings.NewReader(
					`{"device":{"metadata":{"pn_dict_version"`+
						`:"2.0.0","pn_service":"fcm"},"os":"andro`+
						`id","push_notification_token":"synthetic`+
						`-push-token"}}`,
				),
				accept,
			)
		},
		"subscribeDeviceDing": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.SubscribeDeviceDing(ctx, 1000, accept)
		},
		"subscribeDeviceMotion": func(c *generatedhttp.Client) (*http.Response, error) {
			return c.SubscribeDeviceMotion(ctx, 1000, accept)
		},
	}

	return calls
}

func TestAdditionalOperationsPairedReplay(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("fixtures", "http", "synthetic", "paired", "additional-operations.json"))
	require.NoError(t, err)

	var pairs []syntheticOperationPair

	require.NoError(t, json.Unmarshal(data, &pairs))
	require.Len(t, pairs, 21)

	//nolint:bodyclose // The subtest closes each returned response after checking its paired exchange.
	calls := additionalOperationCalls()

	for _, pair := range pairs {
		t.Run(pair.OperationID, func(t *testing.T) {
			t.Parallel()

			call, ok := calls[pair.OperationID]
			require.True(t, ok, "no generated call for fixture")

			transport := replay.NewTransport(pair.Exchange)
			client, createErr := generatedhttp.NewClient(
				pair.Request.Origin,
				generatedhttp.WithHTTPClient(&http.Client{Transport: transport}),
			)
			require.NoError(t, createErr)

			response, callErr := call(client)
			require.NoError(t, callErr)

			t.Cleanup(func() { _ = response.Body.Close() })

			require.Equal(t, pair.Response.Status, response.StatusCode)
			require.Equal(t, pair.Response.Headers, response.Header)
			body, readErr := io.ReadAll(response.Body)
			require.NoError(t, readErr)

			switch {
			case pair.Response.JSON:
				require.True(t, replay.SemanticEqual(pair.Response.Body, body))
			case string(pair.Response.Body) == "null":
				require.Empty(t, body)
			default:
				var expected string

				require.NoError(t, json.Unmarshal(pair.Response.Body, &expected))
				require.Equal(t, expected, string(body))
			}

			require.NoError(t, transport.AssertConsumed())
		})
	}
}
