package main

import (
	"net"
	"strings"
)

type headerField struct {
	name  string
	value string
}

type capturedMessage struct {
	method  string
	host    string
	path    string
	headers []headerField
	content []byte
}

type capturedResponse struct {
	status  int
	headers []headerField
	content []byte
}

type websocketMessage struct {
	opcode     int64
	fromClient bool
	content    []byte
}

type capturedWebSocket struct {
	messages []websocketMessage
}

type capturedFlow struct {
	request   *capturedMessage
	response  *capturedResponse
	websocket *capturedWebSocket
}

func flowFromState(state map[string]any) (*capturedFlow, error) {
	flow := &capturedFlow{request: nil, response: nil, websocket: nil}

	if requestState, exists := state["request"]; exists && requestState != nil {
		requestObject, ok := asObject(requestState)
		if !ok {
			return nil, captureErrorf("invalid mitmproxy request state")
		}

		flow.request = requestFromState(requestObject)
	}

	if responseState, exists := state["response"]; exists && responseState != nil {
		responseObject, ok := asObject(responseState)
		if !ok {
			return nil, captureErrorf("invalid mitmproxy response state")
		}

		response, err := responseFromState(responseObject)
		if err != nil {
			return nil, err
		}

		flow.response = response
	}

	if websocketState, exists := state["websocket"]; exists && websocketState != nil {
		websocketObject, ok := asObject(websocketState)
		if !ok {
			return nil, captureErrorf("invalid mitmproxy WebSocket state")
		}

		websocket, err := websocketFromState(websocketObject)
		if err != nil {
			return nil, err
		}

		flow.websocket = websocket
	}

	return flow, nil
}

func requestFromState(state map[string]any) *capturedMessage {
	request := &capturedMessage{
		method:  stringValue(state["method"]),
		host:    stringValue(state["host"]),
		path:    stringValue(state["path"]),
		headers: headersFromState(state["headers"]),
		content: bytesValue(state["content"]),
	}
	if hostHeader := request.header("host"); hostHeader != "" {
		request.host = prettyHost(hostHeader)
	}

	return request
}

func responseFromState(state map[string]any) (*capturedResponse, error) {
	status, ok := integerValue(state["status_code"])
	if !ok {
		return nil, captureErrorf("invalid mitmproxy response status")
	}

	return &capturedResponse{
		status:  int(status),
		headers: headersFromState(state["headers"]),
		content: bytesValue(state["content"]),
	}, nil
}

func websocketFromState(state map[string]any) (*capturedWebSocket, error) {
	messageStates, ok := state["messages"].([]any)
	if !ok {
		return nil, captureErrorf("invalid mitmproxy WebSocket messages")
	}

	websocket := &capturedWebSocket{messages: make([]websocketMessage, 0, len(messageStates))}

	for _, messageState := range messageStates {
		fields, ok := messageState.([]any)
		if !ok || len(fields) < 3 {
			return nil, captureErrorf("invalid mitmproxy WebSocket message")
		}

		opcode, ok := integerValue(fields[0])
		if !ok {
			return nil, captureErrorf("invalid mitmproxy WebSocket opcode")
		}

		fromClient, ok := fields[1].(bool)
		if !ok {
			return nil, captureErrorf("invalid mitmproxy WebSocket direction")
		}

		websocket.messages = append(websocket.messages, websocketMessage{
			opcode:     opcode,
			fromClient: fromClient,
			content:    bytesValue(fields[2]),
		})
	}

	return websocket, nil
}

func headersFromState(value any) []headerField {
	entries, _ := value.([]any)

	headers := make([]headerField, 0, len(entries))

	for _, entry := range entries {
		fields, ok := entry.([]any)
		if !ok || len(fields) < 2 {
			continue
		}

		headers = append(headers, headerField{name: stringValue(fields[0]), value: stringValue(fields[1])})
	}

	return headers
}

func (message *capturedMessage) header(name string) string {
	value, _ := lookupHeader(message.headers, name)

	return value
}

func lookupHeader(headers []headerField, name string) (string, bool) {
	values := make([]string, 0)

	for _, header := range headers {
		if strings.EqualFold(header.name, name) {
			values = append(values, header.value)
		}
	}

	return strings.Join(values, ", "), len(values) > 0
}

func prettyHost(authority string) string {
	{
		host, _, err := net.SplitHostPort(authority)
		if err == nil {
			return host
		}
	}

	if strings.HasPrefix(authority, "[") {
		if end := strings.IndexByte(authority, ']'); end >= 0 {
			return authority[1:end]
		}
	}

	return authority
}

func stringValue(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return ""
	}
}

func bytesValue(value any) []byte {
	content, _ := value.([]byte)

	return content
}
