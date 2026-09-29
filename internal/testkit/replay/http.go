// Package replay provides strict, local-only transports for deterministic tests.
package replay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Exchange is one recorded request and its response. Bodies are byte-exact unless JSON is true.
type Exchange struct {
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}
type Request struct {
	Method       string          `json:"method"`
	Origin       string          `json:"origin"`
	Path         string          `json:"path"`
	Query        []Pair          `json:"query"`
	Headers      http.Header     `json:"headers"`
	HeadersMode  HeadersMode     `json:"headers_mode,omitempty"`
	Body         json.RawMessage `json:"body"`
	BodyEncoding string          `json:"body_encoding,omitempty"`
	JSON         bool            `json:"json,omitempty"`
}

// HeadersMode controls cassette header matching. Empty mode is exact.
type HeadersMode string

const (
	HeadersExact    HeadersMode = "exact"
	HeadersRequired HeadersMode = "required"
)

type Response struct {
	Status       int             `json:"status"`
	Headers      http.Header     `json:"headers"`
	Body         json.RawMessage `json:"body"`
	BodyEncoding string          `json:"body_encoding,omitempty"`
	JSON         bool            `json:"json,omitempty"`
}
type Pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type noMatchingExchangeError struct {
	method string
	url    string
}

func (replayErr noMatchingExchangeError) Error() string {
	return fmt.Sprintf("replay: no unused exchange matches %s %s", replayErr.method, replayErr.url)
}

type unconsumedExchangesError struct{ exchanges string }

func (e unconsumedExchangesError) Error() string {
	return "replay: unconsumed exchanges: " + e.exchanges
}

type multipleJSONValuesError struct{}

func (multipleJSONValuesError) Error() string { return "multiple JSON values" }

type invalidBodyEncodingError struct {
	encoding string
	reason   string
}

func (failure invalidBodyEncodingError) Error() string {
	return "replay: body encoding " + strconv.Quote(failure.encoding) + " " + failure.reason
}

type replayCauseError struct {
	operation string
	cause     error
}

func (failure replayCauseError) Error() string {
	return failure.operation + ": " + failure.cause.Error()
}

func (failure replayCauseError) Unwrap() error { return failure.cause }

func wrapReplayError(operation string, cause error) error {
	return replayCauseError{operation: operation, cause: cause}
}

// LoadExchange decodes a JSON cassette file. Response bodies are represented as JSON strings.
func LoadExchange(path string) (Exchange, error) {
	cassetteBytes, readErr := os.ReadFile(path) // #nosec G304 -- cassette paths are supplied by the test harness.
	if readErr != nil {
		return Exchange{}, wrapReplayError("read HTTP replay cassette", readErr)
	}

	var exchange Exchange

	readErr = json.Unmarshal(cassetteBytes, &exchange)
	if readErr != nil {
		return Exchange{}, wrapReplayError("decode HTTP replay cassette", readErr)
	}

	return exchange, nil
}

// Transport replays each cassette at most once and never dials a network destination.
type Transport struct {
	mu        sync.Mutex
	exchanges []Exchange
	used      []bool
	ordered   bool
	err       error
}

func NewTransport(xs ...Exchange) *Transport {
	return &Transport{exchanges: append([]Exchange(nil), xs...), used: make([]bool, len(xs)), ordered: true}
}

// NewUnorderedTransport permits explicitly independent requests to consume
// cassettes in any order while still requiring a one-time exact match.
func NewUnorderedTransport(xs ...Exchange) *Transport {
	return &Transport{exchanges: append([]Exchange(nil), xs...), used: make([]bool, len(xs))}
}
func (t *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	{
		err := request.Context().Err()
		if err != nil {
			return nil, wrapReplayError("HTTP replay request canceled", err)
		}
	}

	var (
		requestBody []byte
		bodyReadErr error
		err         error
	)

	if request.Body != nil {
		requestBody, bodyReadErr = io.ReadAll(request.Body)
	}

	if bodyReadErr != nil {
		return nil, wrapReplayError("read HTTP replay request body", bodyReadErr)
	}

	if request.Body != nil {
		request.Body = io.NopCloser(bytes.NewReader(requestBody))
	}

	for index, exchange := range t.exchanges {
		err := request.Context().Err()
		if err != nil {
			return nil, wrapReplayError("HTTP replay request canceled", err)
		}

		t.mu.Lock()

		if t.used[index] {
			t.mu.Unlock()

			continue
		}

		if t.ordered {
			first := 0
			for first < len(t.used) && t.used[first] {
				first++
			}

			if index != first {
				t.mu.Unlock()

				break
			}
		}

		ok, _ := matches(exchange.Request, request, requestBody)
		if ok {
			t.used[index] = true
			t.mu.Unlock()

			responseHeaders := exchange.Response.Headers.Clone()
			if responseHeaders == nil {
				responseHeaders = make(http.Header)
			}

			body, err := decodeBodyWithEncoding(exchange.Response.Body, exchange.Response.JSON, exchange.Response.BodyEncoding)
			if err != nil {
				return nil, wrapReplayError("decode HTTP replay response body", err)
			}

			return &http.Response{
				StatusCode:    exchange.Response.Status,
				Status:        fmt.Sprintf("%d %s", exchange.Response.Status, http.StatusText(exchange.Response.Status)),
				Header:        responseHeaders,
				Body:          io.NopCloser(bytes.NewReader(body)),
				ContentLength: int64(len(body)),
				Request:       request,
			}, nil
		}

		t.mu.Unlock()
	}

	err = noMatchingExchangeError{method: request.Method, url: request.URL.String()}

	t.mu.Lock()
	t.err = errors.Join(t.err, err)
	t.mu.Unlock()

	return nil, err
}
func (t *Transport) AssertConsumed() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	var left []string

	for i, x := range t.exchanges {
		if !t.used[i] {
			left = append(left, x.Request.Method+" "+x.Request.Origin+x.Request.Path)
		}
	}

	var errs []error
	if len(left) > 0 {
		errs = append(errs, unconsumedExchangesError{exchanges: strings.Join(left, ", ")})
	}

	if t.err != nil {
		errs = append(errs, t.err)
	}

	return errors.Join(errs...)
}
func matches(expected Request, request *http.Request, requestBody []byte) (bool, string) {
	requestURL := request.URL

	origin := requestURL.Scheme + "://" + requestURL.Host
	if expected.Method != request.Method || expected.Origin != origin || expected.Path != requestURL.EscapedPath() {
		return false, "method/origin/path"
	}

	if !reflect.DeepEqual(sortedPairs(expected.Query), sortedPairs(queryPairs(requestURL.Query()))) {
		return false, "query"
	}

	if !headersMatch(expected.Headers, request.Header, expected.HeadersMode) {
		return false, "headers"
	}

	if expected.JSON {
		if expected.BodyEncoding != "" {
			return false, "json body encoding"
		}

		if !semanticJSONEqual(expected.Body, requestBody) {
			return false, "json body"
		}
	} else {
		expectedBody, err := decodeBodyWithEncoding(expected.Body, false, expected.BodyEncoding)
		if err != nil || !bytes.Equal(expectedBody, requestBody) {
			return false, "body"
		}
	}

	return true, ""
}
func headersMatch(want, got http.Header, mode HeadersMode) bool {
	wantedHeaders, actualHeaders := canonicalHeaders(want), canonicalHeaders(got)

	if mode == "" {
		mode = HeadersExact
	}

	if mode == HeadersExact {
		return reflect.DeepEqual(wantedHeaders, actualHeaders)
	}

	if mode != HeadersRequired {
		return false
	}

	for key, values := range wantedHeaders {
		if !reflect.DeepEqual(values, actualHeaders[key]) {
			return false
		}
	}

	return true
}
func decodeBody(rawBody json.RawMessage, isJSON bool) []byte {
	body, _ := decodeBodyWithEncoding(rawBody, isJSON, "")

	return body
}

func decodeBodyWithEncoding(rawBody json.RawMessage, isJSON bool, encoding string) ([]byte, error) {
	if len(rawBody) == 0 {
		return nil, nil
	}

	if isJSON {
		if encoding != "" {
			return nil, invalidBodyEncodingError{encoding: encoding, reason: "cannot be combined with JSON bodies"}
		}

		return append([]byte(nil), rawBody...), nil
	}

	if string(rawBody) == "null" {
		return nil, nil
	}

	var bodyText string
	if encoding == "base64" {
		decodeErr := json.Unmarshal(rawBody, &bodyText)
		if decodeErr != nil {
			return nil, wrapReplayError("decode base64 body string", decodeErr)
		}

		body, err := base64.StdEncoding.DecodeString(bodyText)
		if err != nil {
			return nil, wrapReplayError("decode base64 body", err)
		}

		return body, nil
	}

	if encoding != "" {
		return nil, invalidBodyEncodingError{encoding: encoding, reason: "is unsupported"}
	}

	if json.Unmarshal(rawBody, &bodyText) == nil {
		return []byte(bodyText), nil
	}

	return append([]byte(nil), rawBody...), nil
}
func queryPairs(values url.Values) []Pair {
	var pairs []Pair

	for key, values := range values {
		for _, value := range values {
			pairs = append(pairs, Pair{key, value})
		}
	}

	return pairs
}
func sortedPairs(pairs []Pair) []Pair {
	sorted := append([]Pair(nil), pairs...)
	sort.Slice(sorted, func(left, right int) bool {
		if sorted[left].Name == sorted[right].Name {
			return sorted[left].Value < sorted[right].Value
		}

		return sorted[left].Name < sorted[right].Name
	})

	return sorted
}
func canonicalHeaders(headers http.Header) map[string][]string {
	canonical := map[string][]string{}

	for headerName, values := range headers {
		key := strings.ToLower(headerName)

		copiedValues := append([]string(nil), values...)
		canonical[key] = append(canonical[key], copiedValues...)
	}

	for key := range canonical {
		sort.Strings(canonical[key])
	}

	return canonical
}
func semanticJSONEqual(expectedJSON, actualJSON []byte) bool {
	decode := func(rawJSON []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(rawJSON))
		decoder.UseNumber()

		var decoded any

		decodeErr := decoder.Decode(&decoded)
		if decodeErr != nil {
			return nil, wrapReplayError("decode JSON for replay matching", decodeErr)
		}

		var extra any
		{
			decodeErr = decoder.Decode(&extra)
			if decodeErr != io.EOF {
				return nil, multipleJSONValuesError{}
			}
		}

		return normalizeNumbers(decoded), nil
	}

	expectedValue, expectedErr := decode(expectedJSON)
	if expectedErr != nil {
		return false
	}

	actualValue, actualErr := decode(actualJSON)

	return actualErr == nil && reflect.DeepEqual(expectedValue, actualValue)
}
func normalizeNumbers(value any) any {
	switch normalizedValue := value.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(normalizedValue))
		if ok {
			return struct{ JSONNumber string }{r.RatString()}
		}

		return struct{ JSONNumber string }{string(normalizedValue)}
	case []any:
		for index := range normalizedValue {
			normalizedValue[index] = normalizeNumbers(normalizedValue[index])
		}

		return normalizedValue
	case map[string]any:
		for key, item := range normalizedValue {
			normalizedValue[key] = normalizeNumbers(item)
		}

		return normalizedValue
	default:
		return value
	}
}
