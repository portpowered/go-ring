package replay_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func TestGetDeviceHistory_Success(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()
	deviceID := "987652"

	history, err := client.GetDeviceHistory(
		ctx,
		ring.GetDeviceHistoryRequest{Auth: ring.AuthContext{AccessToken: "test_token"},
			DeviceID: deviceID,
			Limit:    10,
			Kind:     "",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, history)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "GET", req.Method)
	assert.Contains(t, req.URL, "/doorbots/987652/history")
	assert.Contains(t, req.URL, "limit=10")
}

func TestGetDeviceHistory_WithKind(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()
	deviceID := "987652"

	history, err := client.GetDeviceHistory(
		ctx,
		ring.GetDeviceHistoryRequest{Auth: ring.AuthContext{AccessToken: "test_token"},
			DeviceID: deviceID,
			Limit:    10,
			Kind:     "motion",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, history)

	// Verify request included kind parameter and correct endpoint
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Contains(t, req.URL, "/doorbots/987652/history")
	assert.Contains(t, req.URL, "kind=motion")
	assert.Contains(t, req.URL, "limit=10")
}

func TestGetDeviceHistory_OlderThan(t *testing.T) {
	t.Parallel()

	client, transport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	cursor := int64(1720000000)
	history, err := client.GetDeviceHistory(
		newTestContext(),
		ring.GetDeviceHistoryRequest{
			Auth:      ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			DeviceID:  "987652",
			Limit:     20,
			Kind:      "motion",
			OlderThan: &cursor,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, history)

	requests := transport.GetRequests()
	require.Len(t, requests, 1)
	assert.Contains(t, requests[0].URL, "older_than=1720000000")
	assert.Contains(t, requests[0].URL, "kind=motion")

	negative := int64(-1)
	_, err = client.GetDeviceHistory(
		newTestContext(),
		ring.GetDeviceHistoryRequest{
			Auth:      ring.AuthContext{AccessToken: "test_token"},
			DeviceID:  "987652",
			OlderThan: &negative,
		},
	)
	assert.True(t, ringapimodels.IsBadRequestError(err))
	assert.Len(t, transport.GetRequests(), 1)
}

func TestGetDeviceHistory_NoDeviceID(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()

	history, err := client.GetDeviceHistory(
		ctx,
		ring.GetDeviceHistoryRequest{Auth: ring.AuthContext{AccessToken: "test_token"},
			DeviceID: "",
			Limit:    10,
			Kind:     "",
		},
	)
	assert.Nil(t, history)
	require.Error(t, err)
	assert.True(t, ringapimodels.IsBadRequestError(err))

	// Verify no request was made since validation failed before the API call
	requests := mockTransport.GetRequests()
	assert.Empty(t, requests)
}

func TestGetActiveDings_Success(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()
	activeDings, err := client.GetActiveDings(
		ctx,
		ring.GetActiveDingsRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""}},
	)

	require.NoError(t, err)
	require.NotNil(t, activeDings)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "GET", req.Method)
	assert.Contains(t, req.URL, "/dings/active")
}

func TestGetRecording_Success(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	recordingID := int64(987654321)
	videoData := fakeVideoPayload

	// Set up response with video data
	body := bytes.NewBufferString(videoData)
	mockTransport.SetResponse("GET", "/clients_api/dings/987654321/recording", &http.Response{
		StatusCode:    http.StatusOK,
		Status:        "200 OK",
		Body:          io.NopCloser(body),
		ContentLength: int64(len(videoData)),
		Header: http.Header{
			"Content-Type": []string{"video/mp4"},
		},
	})

	ctx := newTestContext()
	stream, err := client.GetRecording(
		ctx,
		ring.GetRecordingRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			RecordingID: recordingID,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, stream)

	defer func() { _ = stream.Body.Close() }()

	assert.Equal(t, "video/mp4", stream.ContentType)
	assert.Equal(t, int64(len(videoData)), stream.ContentLen)
	assert.NotNil(t, stream.Body)
	assert.NotNil(t, stream.Headers)

	// Verify request was made correctly
	requests := mockTransport.GetRequests()
	require.Len(t, requests, 1)
	req := requests[0]
	assert.Equal(t, "GET", req.Method)
	assert.Contains(t, req.URL, "/dings/987654321/recording")
}

func TestGetRecording_NotFound(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	// Set up 404 response
	mockTransport.SetResponse("GET", "/clients_api/dings/999999/recording", &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Body:       io.NopCloser(bytes.NewBufferString("recording not found")),
		Header:     make(http.Header),
	})

	ctx := newTestContext()
	stream, err := client.GetRecording(
		ctx,
		ring.GetRecordingRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			RecordingID: 999999,
		},
	)

	assert.Nil(t, stream)
	require.Error(t, err)
	// The REST client returns HTTPError for 404 status codes
	assert.True(t, ringapimodels.IsHTTPError(err))

	var httpErr *ringapimodels.HTTPError
	if assert.ErrorAs(t, err, &httpErr) {
		assert.Equal(t, 404, httpErr.StatusCode)
	}
}

func TestGetLastRecordingID_Success(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	ctx := newTestContext()
	deviceID := "987652"

	recordingID, err := client.GetLastRecordingID(
		ctx,
		ring.GetLastRecordingIDRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			DeviceID: deviceID,
		},
	)
	require.NoError(t, err)
	assert.Positive(t, recordingID)

	// Verify request was made
	requests := mockTransport.GetRequests()
	require.NotEmpty(t, requests)
}

func TestGetLastRecordingID_NoRecordings(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	// Set up empty history response - API returns an array directly, not wrapped
	mockTransport.SetResponseWithBody("GET", "/clients_api/doorbots/987652/history", http.StatusOK, []interface{}{})

	ctx := newTestContext()
	deviceID := "987652"

	recordingID, err := client.GetLastRecordingID(
		ctx,
		ring.GetLastRecordingIDRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			DeviceID: deviceID,
		},
	)

	assert.Equal(t, int64(0), recordingID)
	require.Error(t, err)
	assert.True(t, ringapimodels.IsNotFoundError(err))
}

func TestGetRecording_CanReadBody(t *testing.T) {
	t.Parallel()

	client, mockTransport := newTestClientWithMockTransport()

	defer func() { _ = client.Close() }()

	recordingID := int64(987654321)
	videoData := fakeVideoPayload

	// Set up response with video data
	mockTransport.SetResponse("GET", "/clients_api/dings/987654321/recording", &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(bytes.NewBufferString(videoData)),
		Header: http.Header{
			"Content-Type": []string{"video/mp4"},
		},
	})

	ctx := newTestContext()
	stream, err := client.GetRecording(
		ctx,
		ring.GetRecordingRequest{Auth: ring.AuthContext{AccessToken: "test_token", HardwareID: ""},
			RecordingID: recordingID,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, stream)

	defer func() { _ = stream.Body.Close() }()

	// Read the body to verify it works
	bodyBytes, err := io.ReadAll(stream.Body)
	require.NoError(t, err)
	assert.Equal(t, videoData, string(bodyBytes))
}
