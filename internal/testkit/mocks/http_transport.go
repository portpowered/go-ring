package mocks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const ringOAuthFixtureFilename = "ring_oauth.json"

// RequestRecord represents a recorded HTTP request.
type RequestRecord struct {
	Method      string
	URL         string
	Headers     http.Header
	Body        []byte
	BodyString  string
	QueryParams map[string]string
}

// MockTransport is a mock HTTP RoundTripper that records requests and returns fixture-based responses.
type MockTransport struct {
	mu              sync.RWMutex
	requests        []*RequestRecord
	responses       map[string]*http.Response
	fixtureDir      string
	defaultResponse *http.Response
}

// NewMockTransport creates a new MockTransport.
func NewMockTransport(fixtureDir string) *MockTransport {
	return &MockTransport{
		mu:              sync.RWMutex{},
		requests:        make([]*RequestRecord, 0),
		responses:       make(map[string]*http.Response),
		fixtureDir:      fixtureDir,
		defaultResponse: nil,
	}
}

// RoundTrip implements http.RoundTripper.
func (m *MockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Record the request
	_ = m.recordRequest(req)

	// Try to find a matching response
	key := m.getRequestKey(req)
	m.mu.RLock()
	response, exists := m.responses[key]
	m.mu.RUnlock()

	if exists {
		// Return the pre-configured response
		return response, nil
	}

	// Try to load from fixture based on URL pattern
	response = m.loadFixtureResponse(req)
	if response != nil {
		return response, nil
	}

	// Return default response or 404
	if m.defaultResponse != nil {
		return m.defaultResponse, nil
	}

	return &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Body:       io.NopCloser(bytes.NewBufferString(`{"error": "no fixture found for request"}`)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

// SetResponse sets a specific response for a request pattern.
func (m *MockTransport) SetResponse(method, path string, response *http.Response) {
	key := fmt.Sprintf("%s %s", method, path)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.responses[key] = response
}

// SetResponseWithBody sets a response with JSON body.
func (m *MockTransport) SetResponseWithBody(method, path string, statusCode int, body interface{}) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		panic(fmt.Sprintf("marshal mock response body: %v", err))
	}

	response := &http.Response{
		StatusCode: statusCode,
		Status:     strconv.Itoa(statusCode),
		Body:       io.NopCloser(bytes.NewBuffer(bodyBytes)),
		Header: map[string][]string{
			"Content-Type": {"application/json"},
		},
	}
	m.SetResponse(method, path, response)
}

// SetDefaultResponse sets a default response for unmatched requests.
func (m *MockTransport) SetDefaultResponse(response *http.Response) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.defaultResponse = response
}

// GetRequests returns all recorded requests.
func (m *MockTransport) GetRequests() []*RequestRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	requests := make([]*RequestRecord, len(m.requests))
	copy(requests, m.requests)

	return requests
}

// ClearRequests clears the request history.
func (m *MockTransport) ClearRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = make([]*RequestRecord, 0)
}

// GetRequestCount returns the number of recorded requests.
func (m *MockTransport) GetRequestCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.requests)
}

// FindRequest finds a request matching the criteria.
func (m *MockTransport) FindRequest(method, pathContains string) *RequestRecord {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, req := range m.requests {
		if req.Method == method && strings.Contains(req.URL, pathContains) {
			return req
		}
	}

	return nil
}

// recordRequest records the request for later assertions.
func (m *MockTransport) recordRequest(req *http.Request) *RequestRecord {
	var bodyBytes []byte
	if req.Body != nil {
		bodyBytes, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Restore body for actual use
	}

	record := &RequestRecord{
		Method:      req.Method,
		URL:         req.URL.String(),
		Headers:     req.Header.Clone(),
		Body:        bodyBytes,
		BodyString:  string(bodyBytes),
		QueryParams: make(map[string]string),
	}

	// Parse query parameters
	for key, values := range req.URL.Query() {
		if len(values) > 0 {
			record.QueryParams[key] = values[0]
		}
	}

	m.mu.Lock()
	m.requests = append(m.requests, record)
	m.mu.Unlock()

	return record
}

// getRequestKey generates a key for request matching.
func (m *MockTransport) getRequestKey(req *http.Request) string {
	return fmt.Sprintf("%s %s", req.Method, req.URL.Path)
}

// loadFixtureResponse loads a response from a fixture file based on the request.
func (m *MockTransport) loadFixtureResponse(req *http.Request) *http.Response {
	fixtureName := m.getFixtureName(req)
	if fixtureName == "" {
		return nil
	}

	fixturePath := filepath.Join(m.fixtureDir, fixtureName)

	data, err := os.ReadFile(
		fixturePath,
	) // #nosec G304 -- fixtureName is selected from fixed filenames in getFixtureName.
	if err != nil {
		return nil
	}

	// Wrap array responses in the expected structure for certain endpoints
	data = m.wrapFixtureResponse(req, data)

	// Create response with fixture data
	body := bytes.NewBuffer(data)
	header := make(http.Header)
	header.Set("Content-Type", "application/json")

	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(body),
		Header:     header,
		Request:    req,
	}
}

// wrapFixtureResponse wraps array responses in the expected object structure.
func (m *MockTransport) wrapFixtureResponse(req *http.Request, data []byte) []byte {
	if strings.Contains(req.URL.Path, "/device_info/v3/devices") {
		var grouped map[string][]json.RawMessage
		if json.Unmarshal(data, &grouped) == nil {
			devices := make([]json.RawMessage, 0)
			for _, key := range []string{"doorbots", "authorized_doorbots", "chimes", "stickup_cams", "other"} {
				devices = append(devices, grouped[key]...)
			}

			wrapped, err := json.Marshal(map[string]any{"devices": devices})
			if err == nil {
				return wrapped
			}
		}
	}

	return data
}

// getFixtureName determines which fixture file to use based on the request.
func (m *MockTransport) getFixtureName(req *http.Request) string {
	path := req.URL.Path
	url := req.URL.String()

	if fixture := m.oauthFixtureName(req, url); fixture != "" {
		return fixture
	}

	if strings.Contains(path, "/session") {
		return "ring_session.json"
	}

	if fixture := deviceFixtureName(path, url); fixture != "" {
		return fixture
	}

	if fixture := m.healthFixtureName(path); fixture != "" {
		return fixture
	}

	if strings.Contains(path, "/dings/active") {
		return "ring_ding_active.json"
	}

	if fixture := historyFixtureName(path); fixture != "" {
		return fixture
	}

	if strings.Contains(path, "/recording") {
		return ""
	}

	if strings.Contains(path, "/listen/credentials") {
		return "ring_listen_credentials.json"
	}

	if fixture := groupFixtureName(path); fixture != "" {
		return fixture
	}

	return intercomFixtureName(path)
}

func (m *MockTransport) oauthFixtureName(req *http.Request, requestURL string) string {
	if !strings.Contains(requestURL, "/oauth/token") || req.Method != http.MethodPost {
		return ""
	}

	// Read and restore the body before selecting the shared token fixture.
	_ = m.readBody(req)

	return ringOAuthFixtureFilename
}

func deviceFixtureName(path, requestURL string) string {
	if !strings.Contains(path, "/ring_devices") && !strings.Contains(path, "/device_info/v3/devices") {
		return ""
	}

	if strings.Contains(requestURL, "updated") {
		return "ring_devices_updated.json"
	}

	return "ring_devices.json"
}

func (m *MockTransport) healthFixtureName(path string) string {
	if !strings.Contains(path, "/health") {
		return ""
	}

	deviceID := m.extractDeviceID(path)
	if deviceID == "987653" {
		return "ring_doorboot_health_attrs_id987653.json"
	}

	if strings.Contains(path, "chime") {
		return "ring_chime_health_attrs.json"
	}

	return "ring_doorboot_health_attrs.json"
}

func historyFixtureName(path string) string {
	if !strings.Contains(path, "/dings/history") &&
		(!strings.Contains(path, "/doorbots/") || !strings.Contains(path, "/history")) {
		return ""
	}

	if strings.Contains(path, "intercom") {
		return "ring_intercom_history.json"
	}

	return "ring_doorbot_history.json"
}

func groupFixtureName(path string) string {
	if !strings.Contains(path, "/groups") {
		return ""
	}

	if strings.Contains(path, "/devices") {
		return "ring_group_devices.json"
	}

	return "ring_groups.json"
}

func intercomFixtureName(path string) string {
	if !strings.Contains(path, "/intercom") {
		return ""
	}

	if strings.Contains(path, "/settings") {
		return "ring_intercom_settings.json"
	}

	if strings.Contains(path, "/users") {
		return "ring_intercom_users.json"
	}

	return ""
}

// readBody reads the request body (helper for getFixtureName)
// Note: This should be called before the body is consumed.
func (m *MockTransport) readBody(req *http.Request) []byte {
	if req.Body == nil {
		return nil
	}

	bodyBytes, _ := io.ReadAll(req.Body)
	if len(bodyBytes) > 0 {
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
	}

	return bodyBytes
}

// extractDeviceID extracts device ID from path.
func (m *MockTransport) extractDeviceID(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "ring_devices" || part == "devices" {
			if i+1 < len(parts) {
				return parts[i+1]
			}
		}
	}

	return ""
}
