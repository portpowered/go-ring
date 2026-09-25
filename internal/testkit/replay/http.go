// Package replay provides strict, local-only transports for deterministic tests.
package replay

import (
	"bytes"
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
	"strings"
	"sync"
)

// Exchange is one recorded request and its response. Bodies are byte-exact unless JSON is true.
type Exchange struct {
	Request  Request  `json:"request"`
	Response Response `json:"response"`
}
type Request struct {
	Method      string          `json:"method"`
	Origin      string          `json:"origin"`
	Path        string          `json:"path"`
	Query       []Pair          `json:"query"`
	Headers     http.Header     `json:"headers"`
	HeadersMode HeadersMode     `json:"headers_mode,omitempty"`
	Body        json.RawMessage `json:"body"`
	JSON        bool            `json:"json,omitempty"`
}

// HeadersMode controls cassette header matching. Empty mode is exact.
type HeadersMode string

const (
	HeadersExact    HeadersMode = "exact"
	HeadersRequired HeadersMode = "required"
)

type Response struct {
	Status  int             `json:"status"`
	Headers http.Header     `json:"headers"`
	Body    json.RawMessage `json:"body"`
	JSON    bool            `json:"json,omitempty"`
}
type Pair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// LoadExchange decodes a JSON cassette file. Response bodies are represented as JSON strings.
func LoadExchange(path string) (Exchange, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return Exchange{}, e
	}
	var x Exchange
	e = json.Unmarshal(b, &x)
	return x, e
}

// Transport replays each cassette at most once and never dials a network destination.
type Transport struct {
	mu        sync.Mutex
	exchanges []Exchange
	used      []bool
	err       error
}

func NewTransport(xs ...Exchange) *Transport {
	return &Transport{exchanges: append([]Exchange(nil), xs...), used: make([]bool, len(xs))}
}
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.Context().Err(); err != nil {
		return nil, err
	}
	var b []byte
	var e error
	var err error
	if r.Body != nil {
		b, e = io.ReadAll(r.Body)
	}
	if e != nil {
		return nil, e
	}
	if r.Body != nil {
		r.Body = io.NopCloser(bytes.NewReader(b))
	}
	for i, x := range t.exchanges {
		if err := r.Context().Err(); err != nil {
			return nil, err
		}
		t.mu.Lock()
		if t.used[i] {
			t.mu.Unlock()
			continue
		}
		ok, _ := matches(x.Request, r, b)
		if ok {
			t.used[i] = true
			t.mu.Unlock()
			h := x.Response.Headers.Clone()
			if h == nil {
				h = make(http.Header)
			}
			body := decodeBody(x.Response.Body, x.Response.JSON)
			return &http.Response{StatusCode: x.Response.Status, Status: fmt.Sprintf("%d %s", x.Response.Status, http.StatusText(x.Response.Status)), Header: h, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
		}
		t.mu.Unlock()
	}
	err = fmt.Errorf("replay: no unused exchange matches %s %s", r.Method, r.URL)
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
		errs = append(errs, fmt.Errorf("replay: unconsumed exchanges: %s", strings.Join(left, ", ")))
	}
	if t.err != nil {
		errs = append(errs, t.err)
	}
	return errors.Join(errs...)
}
func matches(x Request, r *http.Request, b []byte) (bool, string) {
	u := r.URL
	origin := u.Scheme + "://" + u.Host
	if x.Method != r.Method || x.Origin != origin || x.Path != u.EscapedPath() {
		return false, "method/origin/path"
	}
	if !reflect.DeepEqual(sortedPairs(x.Query), sortedPairs(queryPairs(u.Query()))) {
		return false, "query"
	}
	if !headersMatch(x.Headers, r.Header, x.HeadersMode) {
		return false, "headers"
	}
	if x.JSON {
		if !semanticJSONEqual(x.Body, b) {
			return false, "json body"
		}
	} else if !bytes.Equal(decodeBody(x.Body, false), b) {
		return false, "body"
	}
	return true, ""
}
func headersMatch(want, got http.Header, mode HeadersMode) bool {
	w, g := canonicalHeaders(want), canonicalHeaders(got)
	if mode == "" {
		mode = HeadersExact
	}
	if mode == HeadersExact {
		return reflect.DeepEqual(w, g)
	}
	if mode != HeadersRequired {
		return false
	}
	for k, values := range w {
		if !reflect.DeepEqual(values, g[k]) {
			return false
		}
	}
	return true
}
func decodeBody(v json.RawMessage, isJSON bool) []byte {
	if len(v) == 0 {
		return nil
	}
	if isJSON {
		return append([]byte(nil), v...)
	}
	if string(v) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(v, &s) == nil {
		return []byte(s)
	}
	return append([]byte(nil), v...)
}
func queryPairs(v url.Values) []Pair {
	var p []Pair
	for k, vs := range v {
		for _, v := range vs {
			p = append(p, Pair{k, v})
		}
	}
	return p
}
func sortedPairs(p []Pair) []Pair {
	q := append([]Pair(nil), p...)
	sort.Slice(q, func(i, j int) bool {
		if q[i].Name == q[j].Name {
			return q[i].Value < q[j].Value
		}
		return q[i].Name < q[j].Name
	})
	return q
}
func canonicalHeaders(h http.Header) map[string][]string {
	m := map[string][]string{}
	for k, v := range h {
		key := strings.ToLower(k)
		z := append([]string(nil), v...)
		m[key] = append(m[key], z...)
	}
	for key := range m {
		sort.Strings(m[key])
	}
	return m
}
func semanticJSONEqual(a, b []byte) bool {
	decode := func(v []byte) (any, error) {
		d := json.NewDecoder(bytes.NewReader(v))
		d.UseNumber()
		var x any
		e := d.Decode(&x)
		if e != nil {
			return nil, e
		}
		var extra any
		if e = d.Decode(&extra); e != io.EOF {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return normalizeNumbers(x), nil
	}
	x, e := decode(a)
	if e != nil {
		return false
	}
	y, e := decode(b)
	return e == nil && reflect.DeepEqual(x, y)
}
func normalizeNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		r, ok := new(big.Rat).SetString(string(x))
		if ok {
			return struct{ JSONNumber string }{r.RatString()}
		}
		return struct{ JSONNumber string }{string(x)}
	case []any:
		for i := range x {
			x[i] = normalizeNumbers(x[i])
		}
		return x
	case map[string]any:
		for k, z := range x {
			x[k] = normalizeNumbers(z)
		}
		return x
	default:
		return v
	}
}
