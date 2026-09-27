package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"
)

type httpExchange struct {
	Request  requestFixture  `json:"request"`
	Response responseFixture `json:"response"`
}

type requestFixture struct {
	Method      string              `json:"method"`
	Origin      string              `json:"origin"`
	Path        string              `json:"path"`
	Query       []queryFixture      `json:"query"`
	Headers     map[string][]string `json:"headers"`
	HeadersMode string              `json:"headers_mode"`
	Body        any                 `json:"body"`
	JSON        bool                `json:"json"`
}

type responseFixture struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers"`
	Body    any                 `json:"body"`
	JSON    bool                `json:"json"`
}

type queryFixture struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type sessionFixture struct {
	Messages []sessionMessage `json:"messages"`
}

type sessionMessage struct {
	Direction string `json:"direction"`
	Frame     string `json:"frame"`
	Payload   any    `json:"payload"`
}

var safePathPatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`^(/device_info/v3/devices/)[^/]+`), `${1}` + deviceIDPlaceholder},
	{regexp.MustCompile(`^(/devices/v1/devices/)[^/]+`), `${1}` + deviceIDPlaceholder},
	{regexp.MustCompile(`^(/evm/v2/timeline/devices/)[^/]+`), `${1}` + deviceIDPlaceholder},
	{regexp.MustCompile(`^(/evm/v4/timeline/devices/)[^/]+`), `${1}` + deviceIDPlaceholder},
	{regexp.MustCompile(`^(/clients_api/doorbots/)[^/]+`), `${1}` + deviceIDPlaceholder},
	{regexp.MustCompile(`^(/clients_api/dings/)[^/]+`), `${1}{recording_id}`},
	{regexp.MustCompile(`^(/commands/v1/devices/)[^/]+`), `${1}` + deviceIDPlaceholder},
	{regexp.MustCompile(`^(/duos/v1/devices/)[^/]+`), `${1}` + deviceIDPlaceholder},
	{regexp.MustCompile(`^(/location_info/v4/locations/)[^/]+`), `${1}{location_id}`},
	{regexp.MustCompile(`^(/location_info/v3/locations/)[^/]+`), `${1}{location_id}`},
	{regexp.MustCompile(`^(/groups/v1/locations/)[^/]+`), `${1}{location_id}`},
}

var eligiblePatterns = map[string]*regexp.Regexp{
	"device-detail":      regexp.MustCompile(`^/device_info/v3/devices/[^/]+$`),
	"siren":              regexp.MustCompile(`^/clients_api/doorbots/[^/]+/siren_(on|off)$`),
	"device-timeline":    regexp.MustCompile(`^/evm/v2/timeline/devices/[^/]+$`),
	"location-detail":    regexp.MustCompile(`^/location_info/v4/locations/[^/]+$`),
	"groups":             regexp.MustCompile(`^/groups/v1/locations/[^/]+/groups$`),
	"group-devices":      regexp.MustCompile(`^/groups/v1/locations/[^/]+/devices$`),
	"recording-favorite": regexp.MustCompile(`^/clients_api/dings/[^/]+/favorite$`),
	"recording-delete":   regexp.MustCompile(`^/clients_api/dings/[^/]+$`),
	"device-reboot":      regexp.MustCompile(`^/commands/v1/devices/[^/]+$`),
	"duos-update":        regexp.MustCompile(`^/duos/v1/devices/[^/]+/update$`),
}

func repositoryRoot() string {
	_, sourcePath, _, ok := runtime.Caller(0)
	if ok {
		return filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", ".."))
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workingDirectory
}

func extractCapture(sourcePath, outputDirectory string) error {
	// #nosec G304 -- the capture path is the explicit positional CLI argument.
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return wrapCaptureError("read capture", err)
	}
	flows, err := decodeFlowStream(data)
	if err != nil {
		return wrapCaptureError("read mitmproxy capture", err)
	}
	return extractFlows(flows, outputDirectory)
}

func extractFlows(flows []*capturedFlow, outputDirectory string) error {
	httpRecords, orderedNames, err := firstHTTPRecords(flows)
	if err != nil {
		return err
	}
	if err := writeHTTPRecords(httpRecords, orderedNames, outputDirectory); err != nil {
		return err
	}
	variants, variantNames, err := findHTTPVariants(flows, httpRecords)
	if err != nil {
		return err
	}
	if err := writeHTTPVariants(variants, variantNames, httpRecords, outputDirectory); err != nil {
		return err
	}
	return writeSignalingSessions(flows, outputDirectory)
}

func firstHTTPRecords(flows []*capturedFlow) (map[string]httpExchange, []string, error) {
	httpRecords := make(map[string]httpExchange)
	orderedNames := make([]string, 0)
	for _, flow := range flows {
		if flow.request == nil || flow.response == nil {
			continue
		}
		name := eligible(flow)
		if name == "" {
			continue
		}
		if _, exists := httpRecords[name]; exists {
			continue
		}
		record, err := sanitizedExchange(flow)
		if err != nil {
			return nil, nil, err
		}
		httpRecords[name] = record
		orderedNames = append(orderedNames, name)
	}
	return httpRecords, orderedNames, nil
}

func writeHTTPRecords(records map[string]httpExchange, names []string, outputDirectory string) error {
	for _, name := range names {
		if err := writeJSON(filepath.Join(outputDirectory, "http", "captured", name+".json"), records[name]); err != nil {
			return err
		}
	}
	return nil
}

func findHTTPVariants(flows []*capturedFlow, records map[string]httpExchange) (map[string][]httpExchange, []string, error) {
	emitted := make(map[string]map[string]struct{}, len(records))
	for name, record := range records {
		signature, err := variantSignature(record)
		if err != nil {
			return nil, nil, wrapCaptureError("build baseline fixture signature", err)
		}
		emitted[name] = map[string]struct{}{signature: {}}
	}
	variants := make(map[string][]httpExchange)
	variantNames := make([]string, 0)
	for _, flow := range flows {
		if flow.request == nil || flow.response == nil {
			continue
		}
		name := eligible(flow)
		if name == "" {
			continue
		}
		record, err := sanitizedExchange(flow)
		if err != nil {
			return nil, nil, err
		}
		signature, err := variantSignature(record)
		if err != nil {
			return nil, nil, wrapCaptureError("build variant fixture signature", err)
		}
		if emitted[name] == nil {
			emitted[name] = make(map[string]struct{})
		}
		if _, exists := emitted[name][signature]; exists {
			continue
		}
		emitted[name][signature] = struct{}{}
		if _, exists := variants[name]; !exists {
			variantNames = append(variantNames, name)
		}
		variants[name] = append(variants[name], record)
	}
	sort.Strings(variantNames)
	return variants, variantNames, nil
}

func writeHTTPVariants(variants map[string][]httpExchange, names []string, records map[string]httpExchange, outputDirectory string) error {
	for _, name := range names {
		firstIndex := 1
		if _, hasBaseline := records[name]; hasBaseline {
			firstIndex = 2
		}
		for offset, record := range variants[name] {
			fileName := fmt.Sprintf("%s-%02d.json", name, firstIndex+offset)
			if err := writeJSON(filepath.Join(outputDirectory, "http", "captured", "variants", fileName), record); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeSignalingSessions(flows []*capturedFlow, outputDirectory string) error {
	for _, flowNumber := range []int{21, 402} {
		if flowNumber > len(flows) {
			return captureErrorf("expected WebSocket flow %d", flowNumber)
		}
		flow := flows[flowNumber-1]
		if flow.websocket == nil {
			return captureErrorf("expected WebSocket flow %d", flowNumber)
		}
		messages := make([]sessionMessage, 0, len(flow.websocket.messages))
		cleaner := newSanitizer()
		for _, message := range flow.websocket.messages {
			if message.opcode != 1 {
				return captureErrorf("unsupported non-text frame in selected session flow %d", flowNumber)
			}
			if !utf8.Valid(message.content) {
				return captureErrorf("unsupported non-JSON text in selected session flow %d", flowNumber)
			}
			payload, err := decodeJSONOrdered(message.content)
			if err != nil {
				return captureErrorf("unsupported non-JSON text in selected session flow %d", flowNumber)
			}
			direction := serverToClient
			if message.fromClient {
				direction = clientToServer
			}
			messages = append(messages, sessionMessage{Direction: direction, Frame: "text", Payload: cleaner.value(payload, "")})
		}
		if err := writeJSON(filepath.Join(outputDirectory, "signaling", "captured", fmt.Sprintf("flow-%d.json", flowNumber)), sessionFixture{Messages: messages}); err != nil {
			return err
		}
	}
	return nil
}

func sanitizedExchange(flow *capturedFlow) (httpExchange, error) {
	requestBody, requestJSON, err := decodeJSONBody(flow.request.content, flow.request.headers)
	if err != nil {
		return httpExchange{}, err
	}
	responseBody, responseJSON, err := decodeJSONBody(flow.response.content, flow.response.headers)
	if err != nil {
		return httpExchange{}, err
	}
	cleaner := newSanitizer()
	query := queryValues(flow.request.path)
	queryFixtureValues := make([]queryFixture, 0, len(query))
	for _, parameter := range query {
		queryFixtureValues = append(queryFixtureValues, queryFixture{Name: parameter[0], Value: cleaner.value(parameter[1], parameter[0])})
	}
	requestBody = cleaner.value(requestBody, "")
	responseBody = cleaner.value(responseBody, "")
	accept, foundAccept := lookupHeader(flow.request.headers, "accept")
	if !foundAccept {
		accept = "application/json"
	}
	requestHeaders := map[string][]string{"Accept": {accept}}
	responseHeaders := make(map[string][]string)
	if responseJSON {
		responseHeaders["Content-Type"] = []string{"application/json"}
	}
	return httpExchange{
		Request: requestFixture{
			Method:      flow.request.method,
			Origin:      "https://" + flow.request.host,
			Path:        safePath(flow.request.path),
			Query:       queryFixtureValues,
			Headers:     requestHeaders,
			HeadersMode: "required",
			Body:        requestBody,
			JSON:        requestJSON,
		},
		Response: responseFixture{
			Status:  flow.response.status,
			Headers: responseHeaders,
			Body:    responseBody,
			JSON:    responseJSON,
		},
	}, nil
}

func safePath(rawPath string) string {
	path, _ := splitPathAndQuery(rawPath)
	for _, replacement := range safePathPatterns {
		path = replacement.pattern.ReplaceAllString(path, replacement.replacement)
	}
	return path
}

func eligible(flow *capturedFlow) string {
	if flow.request == nil || flow.response == nil {
		return ""
	}
	host := flow.request.host
	path, _ := splitPathAndQuery(flow.request.path)
	if host == "api.ring.com" {
		switch {
		case path == deviceListPath:
			return "device-list"
		case eligiblePatterns["device-detail"].MatchString(path):
			return "device-detail"
		case strings.HasSuffix(path, "/settings") && strings.HasPrefix(path, "/devices/v1/devices/"):
			return "device-settings-" + strings.ToLower(flow.request.method)
		case eligiblePatterns["siren"].MatchString(path):
			return "siren-" + path[strings.LastIndex(path, "_")+1:]
		case path == "/evm/v3/history/devices":
			return "history-devices"
		case eligiblePatterns["device-timeline"].MatchString(path):
			return "device-timeline"
		case path == "/location_info/v3/locations":
			return "location-list"
		case eligiblePatterns["location-detail"].MatchString(path):
			return "location-detail"
		case eligiblePatterns["groups"].MatchString(path):
			return "groups"
		case eligiblePatterns["group-devices"].MatchString(path):
			return "group-devices"
		case eligiblePatterns["recording-favorite"].MatchString(path):
			return "recording-favorite"
		case eligiblePatterns["recording-delete"].MatchString(path):
			return "recording-delete"
		case eligiblePatterns["device-reboot"].MatchString(path):
			return "device-reboot"
		case eligiblePatterns["duos-update"].MatchString(path):
			return "duos-update"
		}
	}
	if host == "prd-api-us.prd.rings.solutions" && path == "/api/v1/clap/tickets" {
		return "bootstrap-ticket"
	}
	return ""
}

func splitPathAndQuery(rawPath string) (string, string) {
	path := rawPath
	if schemeAt := strings.Index(path, "://"); schemeAt >= 0 {
		if parsed, err := url.Parse(path); err == nil {
			return parsed.Path, parsed.RawQuery
		}
	}
	if fragmentAt := strings.IndexByte(path, '#'); fragmentAt >= 0 {
		path = path[:fragmentAt]
	}
	if queryAt := strings.IndexByte(path, '?'); queryAt >= 0 {
		return path[:queryAt], path[queryAt+1:]
	}
	return path, ""
}

func queryValues(rawPath string) [][2]string {
	_, rawQuery := splitPathAndQuery(rawPath)
	if rawQuery == "" {
		return nil
	}
	parameters := make([][2]string, 0)
	for _, field := range strings.Split(rawQuery, "&") {
		name, value, hasValue := strings.Cut(field, "=")
		if !hasValue {
			value = ""
		}
		parameters = append(parameters, [2]string{queryUnescape(name), queryUnescape(value)})
	}
	return parameters
}

func queryUnescape(value string) string {
	value = strings.ReplaceAll(value, "+", " ")
	decoded, err := url.QueryUnescape(value)
	if err == nil {
		return decoded
	}
	// urllib.parse.parse_qsl leaves malformed percent escapes in place.
	return value
}

func variantSignature(record httpExchange) (string, error) {
	shape := map[string]any{
		"method":         record.Request.Method,
		"query":          record.Request.Query,
		"request_json":   record.Request.JSON,
		"request_body":   canonicalize(record.Request.Body),
		"status":         record.Response.Status,
		"response_json":  record.Response.JSON,
		"response_shape": valueShape(record.Response.Body),
	}
	encoded, err := json.Marshal(shape)
	if err != nil {
		return "", wrapCaptureError("marshal variant signature", err)
	}
	return string(encoded), nil
}

func canonicalize(value any) any {
	switch value := value.(type) {
	case *orderedObject:
		canonical := make(map[string]any, len(value.values))
		for key, child := range value.values {
			canonical[key] = canonicalize(child)
		}
		return canonical
	case map[string]any:
		canonical := make(map[string]any, len(value))
		for key, child := range value {
			canonical[key] = canonicalize(child)
		}
		return canonical
	case []any:
		canonical := make([]any, len(value))
		for index, child := range value {
			canonical[index] = canonicalize(child)
		}
		return canonical
	default:
		return value
	}
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), fixtureDirectoryMode); err != nil {
		return wrapCaptureError("create fixture directory", err)
	}
	// #nosec G304 -- output path is derived from the explicit CLI output directory.
	file, err := os.Create(path)
	if err != nil {
		return wrapCaptureError("create fixture", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		return wrapCaptureError("write fixture", err)
	}
	if err := file.Close(); err != nil {
		return wrapCaptureError("close fixture", err)
	}
	return nil
}
