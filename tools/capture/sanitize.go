package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

var (
	privateKeyPattern   = regexp.MustCompile(`(?i)token|secret|password|credential|authorization|cookie|email|phone|address|postal|post.?code|zip|latitude|longitude|coordinate|(?:^|_)(?:lat|lon|lng)$|serial|mac|bssid|ssid|fingerprint|ice.?pwd|ice.?ufrag|usernamefragment|private.?key|nonce|cursor|pagination|continuation|page.?token|(?:^|_)auth(?:_|$)|(?:^|_)sid$|device.?id|doorbot.?id|location(?:.?id)?|owner|user.?id|account.?id|uuid|session.?id|dialog.?id|riid|command.?id|ticket|cell.?id|ding.?id|ip.?address|(?:^|_)ip$|(?:^|_)id$|(?:^|_)(?:name|description|text|host.?name|host|region|gateway|timezone|network.?name)$`)
	privateTextPattern  = regexp.MustCompile(`(?i)(?:[\w.+-]+@[\w.-]+\.[A-Za-z]{2,}|\b\+?\d[\d ()-]{7,}\d\b|\b(?:\d{1,3}\.){3}\d{1,3}\b|\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b|https?://[^\s"']+|(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\s*[:=]\s*[^\s,;]+|-?\d{1,3}\.\d+\s*[,/]\s*-?\d{1,3}\.\d+|\b\d{1,6}\s+[\w .'-]+\b(?:street|st|road|rd|avenue|ave|boulevard|blvd|lane|ln|drive|dr)\b)`)
	ipv4BoundaryPattern = regexp.MustCompile(`(^|[^\w.])((?:\d{1,3}\.){3}\d{1,3})($|[^\w.])`)
	ipv6Pattern         = regexp.MustCompile(`(?i)(?:[0-9a-f]{1,4}:){2,}[0-9a-f:]+`)
	localDomainPattern  = regexp.MustCompile(`(?i)\b[a-z0-9-]+\.local\b`)
	uuidPattern         = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}\b`)
	macPattern          = regexp.MustCompile(`(?i)(?:[0-9a-f]{2}:){5}[0-9a-f]{2}`)
	sdpSSRCIDPattern    = regexp.MustCompile(`a=ssrc:\d+`)
	sdpCNAMEPattern     = regexp.MustCompile(`cname:[^ ]+`)
)

var safeFields = map[string]struct{}{
	"method": {}, "jsonrpc": {}, "direction": {}, "reason": {}, "type": {}, "kind": {}, "status": {},
	"command_name": {}, "model": {}, "firmware": {}, "device_type": {}, "device_family": {}, "protocol": {},
	"content_type": {}, "codec": {}, "mid": {}, "setup": {}, "fingerprint_type": {}, "network_type": {},
	"candidate_type": {}, "sdp_type": {}, "version": {}, "source": {}, "event": {}, "event_type": {},
	"notification_type": {}, "notification_scope": {}, "source_type": {}, "action": {}, "role": {}, "state": {},
}

var safeTextEnums = map[string]struct{}{"camera_connected": {}}

type sanitizer struct {
	identifiers      map[string]string
	numericIDs       map[string]int64
	times            map[string]int64
	identifierCounts map[string]int64
	numericCounts    map[string]int64
}

func newSanitizer() *sanitizer {
	return &sanitizer{
		identifiers:      make(map[string]string),
		numericIDs:       make(map[string]int64),
		times:            make(map[string]int64),
		identifierCounts: make(map[string]int64),
		numericCounts:    make(map[string]int64),
	}
}

func (cleaner *sanitizer) value(value any, key string) any {
	switch value := value.(type) {
	case *orderedObject:
		result := newOrderedObject()
		_, hasDeviceID := value.values[deviceIDKey]
		for _, childKey := range value.keys {
			childKeyForValue := childKey
			if childKey == "id" && hasDeviceID {
				childKeyForValue = deviceIDKey
			}
			result.set(childKey, cleaner.value(value.values[childKey], childKeyForValue))
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(value))
		keys := objectKeys(value)
		// Go maps do not retain insertion order. Sorting gives hand-built values
		// deterministic output; captured JSON uses orderedObject above.
		sort.Strings(keys)
		_, hasDeviceID := value[deviceIDKey]
		for _, childKey := range keys {
			childKeyForValue := childKey
			if childKey == "id" && hasDeviceID {
				childKeyForValue = deviceIDKey
			}
			result[childKey] = cleaner.value(value[childKey], childKeyForValue)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			result[index] = cleaner.value(item, key)
		}
		return result
	case string:
		return cleaner.stringValue(value, key)
	case json.Number:
		return cleaner.numberValue(value, key)
	case int:
		return cleaner.numberValue(json.Number(strconv.Itoa(value)), key)
	case int64:
		return cleaner.numberValue(json.Number(strconv.FormatInt(value, 10)), key)
	case float64:
		return cleaner.numberValue(json.Number(strconv.FormatFloat(value, 'g', -1, 64)), key)
	case float32:
		return cleaner.numberValue(json.Number(strconv.FormatFloat(float64(value), 'g', -1, 32)), key)
	default:
		return value
	}
}

func (cleaner *sanitizer) stringValue(value, key string) any {
	lowerKey := strings.ToLower(key)
	if lowerKey == "text" {
		if _, safe := safeTextEnums[value]; safe {
			return value
		}
	}
	if isTimeKey(lowerKey) {
		if strings.ContainsAny(value, "-T:") {
			return "2026-01-01T00:00:00Z"
		}
		return "1700000000000"
	}
	if privateKeyPattern.MatchString(key) && !isSafeField(lowerKey) {
		return cleaner.identifier(key, value)
	}
	if lowerKey == "id" || lowerKey == requestIDKey {
		return cleaner.identifier(key, value)
	}
	if (lowerKey == "sdp" || lowerKey == "description") && (strings.Contains(value, "v=0") || strings.Contains(value, "a=ice-")) {
		return cleaner.sdp(value)
	}
	if (lowerKey == "candidate" || lowerKey == "candidates" || lowerKey == "ice") && strings.Contains(value, " typ ") {
		return cleanICECandidate(value)
	}
	if privateTextPattern.MatchString(value) && !isSafeField(lowerKey) {
		return "sanitized-text"
	}
	return value
}

func (cleaner *sanitizer) numberValue(value json.Number, key string) any {
	lowerKey := strings.ToLower(key)
	integer, isInteger := integerNumber(value)
	if isInteger && privateKeyPattern.MatchString(key) && !isSafeField(lowerKey) {
		return cleaner.numericIdentifier(key, integer.String())
	}
	if !isInteger && privateKeyPattern.MatchString(key) && !isSafeField(lowerKey) {
		return float64(0)
	}
	if isInteger && isTimeKey(lowerKey) {
		return cleaner.numericTime(integer.String())
	}
	if isInteger && integer.IsInt64() {
		return integer.Int64()
	}
	if !isInteger {
		if number, err := value.Float64(); err == nil {
			return number
		}
	}
	return value
}

func integerNumber(value json.Number) (*big.Int, bool) {
	text := string(value)
	if strings.ContainsAny(text, ".eE") {
		return nil, false
	}
	integer, ok := new(big.Int).SetString(text, 10)
	return integer, ok
}

func (cleaner *sanitizer) identifier(key, value string) string {
	domain := strings.ToLower(key)
	domain = regexp.MustCompile(`[^a-z]`).ReplaceAllString(domain, "")
	prefix := "opaque"
	switch {
	case strings.Contains(domain, "dialog"):
		prefix = "dialog"
	case strings.Contains(domain, "riid") || strings.Contains(domain, "route"):
		prefix = "route"
	case strings.Contains(domain, "command") || domain == "id" || domain == requestIDKey:
		prefix = "command"
	case strings.Contains(domain, "session"):
		prefix = "session"
	case strings.Contains(domain, "device") || strings.Contains(domain, "doorbot"):
		prefix = "device"
	case strings.Contains(domain, locationIdentifier):
		prefix = locationIdentifier
	case strings.Contains(domain, "ticket"):
		prefix = "ticket"
	}
	identity := prefix + "\x00" + value
	if existing, exists := cleaner.identifiers[identity]; exists {
		return existing
	}
	cleaner.identifierCounts[prefix]++
	redacted := fmt.Sprintf("%s-%d", prefix, cleaner.identifierCounts[prefix])
	cleaner.identifiers[identity] = redacted
	return redacted
}

func (cleaner *sanitizer) numericIdentifier(key, value string) int64 {
	domain := regexp.MustCompile(`[^a-z]`).ReplaceAllString(strings.ToLower(key), "")
	prefix := "opaque"
	switch {
	case strings.Contains(domain, "device") || strings.Contains(domain, "doorbot"):
		prefix = "device"
	case strings.Contains(domain, "user"):
		prefix = "user"
	case strings.Contains(domain, locationIdentifier):
		prefix = locationIdentifier
	}
	identity := prefix + "\x00" + value
	if existing, exists := cleaner.numericIDs[identity]; exists {
		return existing
	}
	redacted := numericIdentifierBase + cleaner.numericCounts[prefix]
	cleaner.numericCounts[prefix]++
	cleaner.numericIDs[identity] = redacted
	return redacted
}

func (cleaner *sanitizer) numericTime(value string) int64 {
	if existing, exists := cleaner.times[value]; exists {
		return existing
	}
	integer, ok := new(big.Int).SetString(value, 10)
	base := unixSecondsBase
	if !ok || integer.Cmp(big.NewInt(millisecondsThreshold)) >= 0 {
		base = unixMillisecondsBase
	}
	redacted := base + int64(len(cleaner.times))
	cleaner.times[value] = redacted
	return redacted
}

func (cleaner *sanitizer) sdp(value string) string {
	lines := splitLines(value)
	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, "o="):
			fields := strings.Fields(line)
			if len(fields) >= sdpOriginFieldCount {
				address := "::"
				if fields[4] == "IP4" {
					address = "0.0.0.0"
				}
				line = fmt.Sprintf("o=- 1 1 IN %s %s", fields[4], address)
			}
		case strings.HasPrefix(line, "a=ice-ufrag:"):
			line = "a=ice-ufrag:syntheticufrag"
		case strings.HasPrefix(line, "a=ice-pwd:"):
			line = "a=ice-pwd:syntheticicepassword0123456789"
		case strings.HasPrefix(line, "a=fingerprint:"):
			algorithm := strings.SplitN(strings.TrimPrefix(line, "a=fingerprint:"), " ", 2)[0]
			width := 20
			if strings.EqualFold(algorithm, "sha-256") {
				width = 32
			}
			parts := make([]string, width)
			for item := range parts {
				parts[item] = "00"
			}
			line = "a=fingerprint:" + algorithm + " " + strings.Join(parts, ":")
		case strings.HasPrefix(line, "a=candidate:"):
			line = cleanICECandidate(line)
		case strings.HasPrefix(line, "c=IN IP4 ") || strings.HasPrefix(line, "c=IN IP6 "):
			line = line[:strings.LastIndexByte(line, ' ')]
			if strings.Contains(line, "IP4") {
				line += " 0.0.0.0"
			} else {
				line += " ::"
			}
		case strings.HasPrefix(line, "a=ssrc:"):
			line = sdpSSRCIDPattern.ReplaceAllString(line, "a=ssrc:123456")
			line = sdpCNAMEPattern.ReplaceAllString(line, "cname:syntheticcname")
		case strings.HasPrefix(line, "a=msid:"):
			line = "a=msid:syntheticstream synthetictrack"
		}
		if strings.HasPrefix(line, "o=") || strings.HasPrefix(line, "c=") || strings.HasPrefix(line, "a=candidate:") || strings.HasPrefix(line, "a=rtcp:") || strings.HasPrefix(line, "a=remote-candidates:") {
			line = replaceIPv4(line, "192.0.2.1")
			line = ipv6Pattern.ReplaceAllString(line, "2001:db8::1")
			line = localDomainPattern.ReplaceAllString(line, "synthetic.local")
		}
		line = uuidPattern.ReplaceAllString(line, "synthetic-id")
		lines[index] = line
	}
	return strings.Join(lines, "\r\n") + lineEnding(value)
}

func splitLines(value string) []string {
	if value == "" {
		return nil
	}
	lines := strings.Split(value, "\n")
	if strings.HasSuffix(value, "\n") {
		lines = lines[:len(lines)-1]
	}
	for index, line := range lines {
		lines[index] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

func lineEnding(value string) string {
	if strings.HasSuffix(value, "\n") || strings.HasSuffix(value, "\r") {
		return "\r\n"
	}
	return ""
}

func replaceIPv4(value, replacement string) string {
	matches := ipv4BoundaryPattern.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value
	}
	var output strings.Builder
	last := 0
	for _, match := range matches {
		output.WriteString(value[last:match[0]])
		output.WriteString(value[match[2]:match[3]])
		output.WriteString(replacement)
		output.WriteString(value[match[6]:match[7]])
		last = match[1]
	}
	output.WriteString(value[last:])
	return output.String()
}

func cleanICECandidate(value string) string {
	value = replaceIPv4(value, "192.0.2.1")
	value = ipv6Pattern.ReplaceAllString(value, "2001:db8::1")
	value = localDomainPattern.ReplaceAllString(value, "synthetic.local")
	return macPattern.ReplaceAllString(value, "00:00:00:00:00:00")
}

func isTimeKey(key string) bool {
	return key == "timestamp" || key == "time" || key == "date" || strings.HasSuffix(key, "_time") || strings.HasSuffix(key, "_at")
}

func isSafeField(key string) bool {
	_, safe := safeFields[key]
	return safe
}

func decodeJSONBody(content []byte, headers []headerField) (any, bool, error) {
	if len(content) == 0 {
		return nil, false, nil
	}
	decoded, err := decodeContentEncoding(content, headerValue(headers, "content-encoding"))
	if err != nil {
		// mitmproxy get_text(strict=False) returns the original body when content
		// encoding is invalid.
		decoded = content
	}
	decoded = decodeTextEncoding(decoded, headerValue(headers, "content-type"))
	value, err := decodeJSONOrdered(decoded)
	if err != nil {
		return nil, false, captureErrorf("selected API body is not valid JSON")
	}
	return value, true, nil
}

func headerValue(headers []headerField, name string) string {
	values := make([]string, 0)
	for _, header := range headers {
		if strings.EqualFold(header.name, name) {
			values = append(values, header.value)
		}
	}
	return strings.Join(values, ", ")
}

func decodeContentEncoding(content []byte, value string) ([]byte, error) {
	encodings := strings.Split(value, ",")
	decoded := bytes.Clone(content)
	for index := len(encodings) - 1; index >= 0; index-- {
		encoding := strings.ToLower(strings.TrimSpace(encodings[index]))
		var reader io.ReadCloser
		var err error
		switch encoding {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			reader, err = gzip.NewReader(bytes.NewReader(decoded))
		case "deflate":
			reader, err = zlib.NewReader(bytes.NewReader(decoded))
			if err != nil {
				reader = flate.NewReader(bytes.NewReader(decoded))
				err = nil
			}
		default:
			return content, captureErrorf("unsupported content encoding %q", encoding)
		}
		if err != nil {
			return nil, err
		}
		decoded, err = io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	return decoded, nil
}

func decodeTextEncoding(content []byte, contentType string) []byte {
	mediaType, parameters, err := mimeParseMediaType(contentType)
	if err != nil || mediaType == "" {
		return content
	}
	charset := strings.ToLower(strings.TrimSpace(parameters["charset"]))
	switch charset {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return content
	case "iso-8859-1", "latin1", "latin-1", "windows-1252":
		result := make([]rune, 0, len(content))
		for _, char := range content {
			result = append(result, rune(char))
		}
		return []byte(string(result))
	case "utf-16", "utf-16le", utf16BigEndian:
		littleEndian := charset != utf16BigEndian
		if charset == "utf-16" && len(content) >= 2 {
			if content[0] == 0xfe && content[1] == 0xff {
				littleEndian = false
				content = content[2:]
			} else if content[0] == 0xff && content[1] == 0xfe {
				content = content[2:]
			}
		}
		if len(content)%2 != 0 {
			return content
		}
		units := make([]uint16, len(content)/2)
		for index := range units {
			if littleEndian {
				units[index] = uint16(content[index*2]) | uint16(content[index*2+1])<<byteShiftBits
			} else {
				units[index] = uint16(content[index*2])<<byteShiftBits | uint16(content[index*2+1])
			}
		}
		return []byte(string(utf16.Decode(units)))
	default:
		return content
	}
}

func mimeParseMediaType(value string) (string, map[string]string, error) {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		return "", nil, err
	}
	return mediaType, parameters, nil
}

func sortedShapeSignatures(values []any) []string {
	unique := make(map[string]struct{})
	for _, value := range values {
		shape := valueShape(value)
		encoded, err := json.Marshal(shape)
		if err != nil {
			continue
		}
		unique[string(encoded)] = struct{}{}
	}
	signatures := make([]string, 0, len(unique))
	for signature := range unique {
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)
	return signatures
}

func valueShape(value any) any {
	switch value := value.(type) {
	case *orderedObject:
		keys := append([]string(nil), value.keys...)
		sort.Strings(keys)
		shape := make(map[string]any, len(keys))
		for _, key := range keys {
			shape[key] = valueShape(value.values[key])
		}
		return shape
	case map[string]any:
		shape := make(map[string]any, len(value))
		for key, child := range value {
			shape[key] = valueShape(child)
		}
		return shape
	case []any:
		shapes := make([]any, 0)
		for _, signature := range sortedShapeSignatures(value) {
			var shape any
			if err := json.Unmarshal([]byte(signature), &shape); err == nil {
				shapes = append(shapes, shape)
			}
		}
		return map[string]any{"array": shapes}
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, json.Number:
		return "number"
	default:
		return "string"
	}
}
