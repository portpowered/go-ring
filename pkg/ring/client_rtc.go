package ring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// RTCStream represents an active RTC stream
type RTCStream struct {
	streamID       string
	deviceID       string
	sessionID      string
	dialogID       string
	ctx            context.Context
	cancel         context.CancelFunc
	wsConn         *websocket.Conn
	sdpOffer       string
	sdpAnswer      string
	mu             sync.RWMutex
	writeMu        sync.Mutex // Protects concurrent writes to wsConn
	sdpAnswerChan  chan string
	sdpAnswerEvent *sync.Cond
	closeChan      chan error
	isAlive        bool
	lastKeepAlive  time.Time
	pingTicker     *time.Ticker
	readerDone     chan struct{}
	wg             sync.WaitGroup
	iceCandidates  map[int][]string // m-line index -> candidates
}

// StartRTCStream starts a WebRTC stream for live video from a device
// req.SDPOffer is the SDP offer from the caller
func (c *Client) StartRTCStream(ctx context.Context, req StartRTCStreamRequest) (*RTCStream, error) {
	// Convert string deviceID to int64 for Ring API (doorbot_id)
	// Get ticket from Ring API
	token, err := c.getToken(ctx)
	if err != nil {
		return nil, ringapimodels.NewTokenError("failed to get token for RTC stream", err)
	}

	// Request streaming ticket via HTTP request to Ring API
	var ticketResp struct {
		Ticket string `json:"ticket"`
	}

	// Make HTTP request to get ticket
	ticketURL := ringapimodels.RingAppAPIURI + ringapimodels.RingRTCStreamingTicketEndpoint
	httpReq, err := http.NewRequestWithContext(ctx, "POST", ticketURL, nil)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to create ticket request", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("User-Agent", c.userAgent)
	httpReq.Header.Set("Content-Type", "application/json")
	if c.hardwareID != "" {
		httpReq.Header.Set("hardware_id", c.hardwareID)
	}

	// Use the HTTP client from the rest client
	httpClient := c.restClient.HTTPClient()
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, ringapimodels.NewNetworkError("failed to request ticket", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, ringapimodels.NewHTTPError(resp, string(bodyBytes))
	}

	if err := json.NewDecoder(resp.Body).Decode(&ticketResp); err != nil {
		return nil, ringapimodels.NewBadRequestError("failed to decode ticket response", err)
	}

	if ticketResp.Ticket == "" {
		return nil, ringapimodels.NewConnectionError("empty ticket received from Ring API", nil)
	}

	// Create WebSocket connection
	// Use configured websocket URL if set (for testing), otherwise use default
	// Format: client_id (UUID) and token (ticket)
	clientID := uuid.New().String()
	wsURL := strings.Replace(c.rtcWebSocketURL, "{client_id}", clientID, 1)
	wsURL = strings.Replace(wsURL, "{token}", ticketResp.Ticket, 1)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	header := make(map[string][]string)
	header["User-Agent"] = []string{c.userAgent}
	if c.hardwareID != "" {
		header["hardware_id"] = []string{c.hardwareID}
	}

	wsConn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, ringapimodels.NewConnectionError("failed to connect to RTC streaming websocket", err)
	}

	streamCtx, cancel := context.WithCancel(ctx)
	dialogID := uuid.New().String()

	stream := &RTCStream{
		deviceID:      req.DeviceID, // Store as string
		dialogID:      dialogID,
		sdpOffer:      req.SDPOffer,
		ctx:           streamCtx,
		cancel:        cancel,
		wsConn:        wsConn,
		sdpAnswerChan: make(chan string, 1),
		closeChan:     make(chan error, 1),
		isAlive:       true,
		lastKeepAlive: time.Now(),
		readerDone:    make(chan struct{}),
		iceCandidates: make(map[int][]string),
	}
	stream.sdpAnswerEvent = sync.NewCond(&stream.mu)

	// Start reader goroutine
	stream.wg.Add(1)
	go stream.reader()

	// Send live_view offer message
	offerMsg := map[string]interface{}{
		"method":    "live_view",
		"dialog_id": dialogID,
		"body": map[string]interface{}{
			"doorbot_id": req.DeviceID,
			"stream_options": map[string]bool{
				"audio_enabled": false,
				"video_enabled": true,
			},
			"sdp":  req.SDPOffer,
			"type": "offer",
		},
	}

	msgBytes, err := json.Marshal(offerMsg)
	if err != nil {
		cancel()
		wsConn.Close()
		return nil, ringapimodels.NewBadRequestError("failed to marshal offer message", err)
	}

	stream.writeMu.Lock()
	err = wsConn.WriteMessage(websocket.TextMessage, msgBytes)
	stream.writeMu.Unlock()
	if err != nil {
		cancel()
		wsConn.Close()
		return nil, ringapimodels.NewConnectionError("failed to send offer message", err)
	}

	// Wait for SDP answer with timeout
	select {
	case sdpAnswer := <-stream.sdpAnswerChan:
		stream.mu.Lock()
		stream.sdpAnswer = sdpAnswer
		stream.mu.Unlock()

		// Start ping task
		stream.pingTicker = time.NewTicker(5 * time.Second)
		stream.wg.Add(1)
		go stream.pinger()

		return stream, nil
	case err := <-stream.closeChan:
		cancel()
		wsConn.Close()
		return nil, err
	case <-time.After(30 * time.Second):
		cancel()
		wsConn.Close()
		return nil, ringapimodels.NewConnectionError("timeout waiting for SDP answer", nil)
	case <-streamCtx.Done():
		wsConn.Close()
		return nil, streamCtx.Err()
	}
}

// reader reads messages from the WebSocket
func (rs *RTCStream) reader() {
	defer rs.wg.Done()
	defer close(rs.readerDone)

	for {
		select {
		case <-rs.ctx.Done():
			return
		default:
			_, message, err := rs.wsConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					// Log error but don't fail - connection might be closing
				}
				return
			}

			var msg map[string]interface{}
			if err := json.Unmarshal(message, &msg); err != nil {
				continue
			}

			rs.handleMessage(msg)
		}
	}
}

// handleMessage handles incoming WebSocket messages
func (rs *RTCStream) handleMessage(msg map[string]interface{}) {
	// fmt.Println("handleMessage", msg)
	method, ok := msg["method"].(string)
	if !ok {
		return
	}

	switch method {
	case "sdp":
		rs.handleSDPAnswer(msg)
	case "session_created":
		rs.handleSessionCreated(msg)
	case "ice":
		rs.handleICECandidate(msg)
	case "notification":
		rs.handleNotification(msg)
	case "close":
		rs.handleClose(msg)
	case "pong":
		rs.handlePong(msg)
	case "camera_started":
		// Camera started - no action needed
	case "camera_options":
		// Camera options - no action needed
	default:
		// Unknown method - ignore
	}
}

// handleSDPAnswer handles SDP answer message
func (rs *RTCStream) handleSDPAnswer(msg map[string]interface{}) {
	body, ok := msg["body"].(map[string]interface{})
	if !ok {
		return
	}

	sdp, ok := body["sdp"].(string)
	if !ok {
		return
	}

	// Force correct SDP answer (as per Python implementation)
	rs.mu.Lock()
	correctedSDP := rs.forceCorrectSDPAnswer(sdp)
	rs.sdpAnswer = correctedSDP
	rs.mu.Unlock()

	// Send answer to channel
	select {
	case rs.sdpAnswerChan <- correctedSDP:
	default:
	}

	// Activate session
	rs.activateSession()
}

// forceCorrectSDPAnswer corrects the SDP answer based on offer
// An offer of recvonly must be answered with sendonly or inactive
func (rs *RTCStream) forceCorrectSDPAnswer(sdpAnswer string) string {
	if rs.sdpOffer == "" {
		return sdpAnswer
	}

	sdpKinds := []string{"audio", "video", "application"}
	sdpDirections := []string{"sendrecv", "sendonly", "recvonly", "inactive"}

	// Build regex pattern
	kindPattern := strings.Join(sdpKinds, "|")
	dirPattern := strings.Join(sdpDirections, "|")
	pattern := fmt.Sprintf("m=(?P<kind>%s)(.|\n)+?a=(?P<direction>%s)(\r|\n|\r\n)", kindPattern, dirPattern)

	re := regexp.MustCompile(pattern)

	// Find all offers
	offerMatches := re.FindAllStringSubmatch(rs.sdpOffer, -1)
	answerMatches := re.FindAllStringSubmatch(sdpAnswer, -1)

	result := sdpAnswer
	for _, offerMatch := range offerMatches {
		offerKind := ""
		offerDir := ""
		for i, name := range re.SubexpNames() {
			if name == "kind" && i < len(offerMatch) {
				offerKind = offerMatch[i]
			}
			if name == "direction" && i < len(offerMatch) {
				offerDir = offerMatch[i]
			}
		}

		if offerDir != "recvonly" {
			continue
		}

		// Find matching answer
		for _, answerMatch := range answerMatches {
			answerKind := ""
			answerDir := ""
			for i, name := range re.SubexpNames() {
				if name == "kind" && i < len(answerMatch) {
					answerKind = answerMatch[i]
				}
				if name == "direction" && i < len(answerMatch) {
					answerDir = answerMatch[i]
				}
			}

			if offerKind == answerKind && answerDir == "sendrecv" {
				// Replace sendrecv with sendonly
				oldStr := answerMatch[0]
				newStr := strings.Replace(oldStr, "a=sendrecv", "a=sendonly", 1)
				result = strings.Replace(result, oldStr, newStr, 1)
			}
		}
	}

	return result
}

// handleSessionCreated handles session created message
func (rs *RTCStream) handleSessionCreated(msg map[string]interface{}) {
	body, ok := msg["body"].(map[string]interface{})
	if !ok {
		return
	}

	sessionID, ok := body["session_id"].(string)
	if ok {
		rs.mu.Lock()
		rs.sessionID = sessionID
		rs.mu.Unlock()
	}
}

// handleICECandidate handles ICE candidate message
func (rs *RTCStream) handleICECandidate(msg map[string]interface{}) {
	body, ok := msg["body"].(map[string]interface{})
	if !ok {
		return
	}

	iceStr, ok := body["ice"].(string)
	if !ok {
		return
	}

	var mlineIndex float64
	if idx, ok := body["mlineindex"].(float64); ok {
		mlineIndex = idx
	} else if idx, ok := body["mlineindex"].(int); ok {
		mlineIndex = float64(idx)
	}

	rs.mu.Lock()
	idx := int(mlineIndex)
	if rs.iceCandidates[idx] == nil {
		rs.iceCandidates[idx] = []string{}
	}
	rs.iceCandidates[idx] = append(rs.iceCandidates[idx], iceStr)
	rs.mu.Unlock()
}

// handleNotification handles notification messages
func (rs *RTCStream) handleNotification(msg map[string]interface{}) {
	body, ok := msg["body"].(map[string]interface{})
	if !ok {
		return
	}

	text, ok := body["text"].(string)
	if !ok {
		return
	}

	if text == "camera_connected" {
		// Send camera options
		cameraOptionsMsg := rs.getSessionMessage("camera_options", map[string]interface{}{
			"stealth_mode": false,
		})

		msgBytes, err := json.Marshal(cameraOptionsMsg)
		if err != nil {
			return
		}

		rs.mu.RLock()
		conn := rs.wsConn
		rs.mu.RUnlock()

		if conn != nil {
			rs.writeMu.Lock()
			conn.WriteMessage(websocket.TextMessage, msgBytes)
			rs.writeMu.Unlock()
		}
	}
}

// handleClose handles close message
func (rs *RTCStream) handleClose(msg map[string]interface{}) {
	rs.mu.Lock()
	rs.isAlive = false
	rs.mu.Unlock()

	// Extract error information from close message
	var errorMsg string
	if body, ok := msg["body"].(map[string]interface{}); ok {
		if reason, ok := body["reason"].(map[string]interface{}); ok {
			// Try to get error text first
			if text, ok := reason["text"].(string); ok && text != "" {
				errorMsg = text
			} else {
				// Fall back to error code if text is not available
				var code int
				if codeFloat, ok := reason["code"].(float64); ok {
					code = int(codeFloat)
				} else if codeInt, ok := reason["code"].(int); ok {
					code = codeInt
				}
				if code != 0 {
					errorMsg = fmt.Sprintf("connection closed with code %d", code)
				}
			}
		}
	}

	// If no error message was extracted, use a default
	if errorMsg == "" {
		errorMsg = "connection closed by remote"
	}

	// Send error to error channel (non-blocking)
	select {
	case rs.closeChan <- ringapimodels.NewConnectionError(errorMsg, nil):
	default:
		// Channel already has an error or is closed, ignore
	}

	rs.Close()
}

// handlePong handles pong message
func (rs *RTCStream) handlePong(msg map[string]interface{}) {
	rs.mu.Lock()
	rs.lastKeepAlive = time.Now()
	rs.mu.Unlock()
}

// activateSession activates the session
func (rs *RTCStream) activateSession() {
	activateMsg := rs.getSessionMessage("activate_session", map[string]interface{}{})

	msgBytes, err := json.Marshal(activateMsg)
	if err != nil {
		return
	}

	rs.mu.RLock()
	conn := rs.wsConn
	rs.mu.RUnlock()

	if conn != nil {
		rs.writeMu.Lock()
		conn.WriteMessage(websocket.TextMessage, msgBytes)
		rs.writeMu.Unlock()
	}

	rs.mu.Lock()
	rs.lastKeepAlive = time.Now()
	rs.mu.Unlock()
}

// pinger sends ping messages to keep the session alive
func (rs *RTCStream) pinger() {
	defer rs.wg.Done()

	for {
		select {
		case <-rs.ctx.Done():
			return
		case <-rs.pingTicker.C:
			rs.mu.RLock()
			isAlive := rs.isAlive
			conn := rs.wsConn
			rs.mu.RUnlock()

			if !isAlive || conn == nil {
				return
			}

			pingMsg := rs.getSessionMessage("ping", map[string]interface{}{})
			msgBytes, err := json.Marshal(pingMsg)
			if err != nil {
				continue
			}

			rs.writeMu.Lock()
			err = conn.WriteMessage(websocket.TextMessage, msgBytes)
			rs.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// getSessionMessage creates a session message
func (rs *RTCStream) getSessionMessage(method string, body map[string]interface{}) map[string]interface{} {
	rs.mu.RLock()
	sessionID := rs.sessionID
	dialogID := rs.dialogID
	deviceID := rs.deviceID
	rs.mu.RUnlock()

	// Convert string deviceID to int64 for Ring API
	deviceIDInt, _ := strconv.ParseInt(deviceID, 10, 64)

	msgBody := map[string]interface{}{
		"doorbot_id": deviceIDInt,
	}

	if sessionID != "" {
		msgBody["session_id"] = sessionID
	}

	for k, v := range body {
		msgBody[k] = v
	}

	return map[string]interface{}{
		"method":    method,
		"dialog_id": dialogID,
		"body":      msgBody,
	}
}

// StopRTCStream stops an active RTC stream
func (c *Client) StopRTCStream(ctx context.Context, req StopRTCStreamRequest) error {
	// The stream is stopped by calling Close() on the RTCStream
	// This method exists for interface compatibility but the actual
	// stream management should be done via the RTCStream.Close() method
	// req.StreamID is not used, but kept for interface compatibility
	return nil
}

// Close closes the RTC stream
func (rs *RTCStream) Close() error {
	rs.mu.Lock()
	if !rs.isAlive {
		rs.mu.Unlock()
		return nil
	}
	rs.isAlive = false
	rs.mu.Unlock()

	rs.cancel()

	if rs.pingTicker != nil {
		rs.pingTicker.Stop()
	}

	rs.mu.Lock()
	conn := rs.wsConn
	rs.mu.Unlock()

	if conn != nil {
		rs.writeMu.Lock()
		closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		conn.WriteMessage(websocket.CloseMessage, closeMsg)
		conn.Close()
		rs.writeMu.Unlock()
	}

	rs.wg.Wait()
	return nil
}

// GetStreamID returns the stream ID (session ID)
func (rs *RTCStream) GetStreamID() string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.sessionID
}

// GetDeviceID returns the device ID
func (rs *RTCStream) GetDeviceID() string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.deviceID
}

// GetSDPAnswer returns the SDP answer
func (rs *RTCStream) GetSDPAnswer() string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return rs.sdpAnswer
}

// OnICECandidate can be called to send ICE candidates to the stream
func (rs *RTCStream) OnICECandidate(candidate string, mlineIndex int) error {
	rs.mu.RLock()
	sessionID := rs.sessionID
	deviceID := rs.deviceID
	conn := rs.wsConn
	rs.mu.RUnlock()

	if conn == nil {
		return ringapimodels.NewConnectionError("websocket connection is nil", nil)
	}

	// Convert string deviceID to int64 for Ring API
	deviceIDInt, err := strconv.ParseInt(deviceID, 10, 64)
	if err != nil {
		return ringapimodels.NewBadRequestError("invalid device ID format", err)
	}

	body := map[string]interface{}{
		"doorbot_id": deviceIDInt,
		"ice":        candidate,
		"mlineindex": mlineIndex,
	}

	if sessionID != "" {
		body["session_id"] = sessionID
	}

	iceMsg := map[string]interface{}{
		"method":    "ice",
		"dialog_id": rs.dialogID,
		"body":      body,
	}

	msgBytes, err := json.Marshal(iceMsg)
	if err != nil {
		return ringapimodels.NewBadRequestError("failed to marshal ICE message", err)
	}

	rs.writeMu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, msgBytes)
	rs.writeMu.Unlock()
	if err != nil {
		return ringapimodels.NewConnectionError("failed to send ICE candidate", err)
	}

	return nil
}
