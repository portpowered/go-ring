package routegate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedRequestRejectsUntrustedFormattedTargets(t *testing.T) {
	t.Parallel()

	withFmt := func(source string) string {
		return strings.Replace(source, "import (", "import (\n\t\"fmt\"", 1)
	}

	tests := []struct {
		name   string
		mutate func(string) string
	}{
		{
			name: "arbitrary formatted authority",
			mutate: func(source string) string {
				source = withFmt(source)

				return strings.Replace(source,
					`routes.NewGetWidgetRequest("https://api.example.test", widgetID)`,
					`routes.NewGetWidgetRequest(fmt.Sprintf("https://%s", "attacker.example"), widgetID)`,
					1,
				)
			},
		},
		{
			name: "formatted full URL appended to generated path",
			mutate: func(source string) string {
				source = withFmt(source)

				return strings.Replace(source,
					`routes.NewGetWidgetRequest("https://api.example.test", widgetID)`,
					`routes.NewGetWidgetRequest(fmt.Sprintf("https://api.example.test/widgets/%s/extra", widgetID), widgetID)`,
					1,
				)
			},
		},
		{
			name: "formatted route wrapped in an unconfigured prefix",
			mutate: func(source string) string {
				source = withFmt(source)

				return strings.Replace(source,
					`routes.NewGetWidgetRequest("https://api.example.test", widgetID)`,
					`routes.NewGetWidgetRequest(fmt.Sprintf("https://api.example.test/%s", "prefix"), widgetID)`,
					1,
				)
			},
		},
		{
			name: "malformed format string in full URL",
			mutate: func(source string) string {
				source = withFmt(source)

				return strings.Replace(source,
					`routes.NewGetWidgetRequest("https://api.example.test", widgetID)`,
					`routes.NewGetWidgetRequest(fmt.Sprintf("https://api.example.test/%s/widgets/%s", widgetID), widgetID)`,
					1,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertRejectedRouteMutation(t, test.mutate(generatedRequestSource))
		})
	}
}

func TestGeneratedRequestRejectsWrappedAppendedAndInvalidPathMutations(t *testing.T) {
	t.Parallel()

	withFmt := func(source string) string {
		return strings.Replace(source, "import (", "import (\n\t\"fmt\"", 1)
	}

	tests := []struct {
		name   string
		mutate func(string) string
	}{
		{
			name: "append path segment",
			mutate: func(source string) string {
				return strings.Replace(source, `req = req.WithContext(ctx)`,
					`req = req.WithContext(ctx)
	req.URL.Path += "/extra"`, 1)
			},
		},
		{
			name: "wrap path with formatted prefix and suffix",
			mutate: func(source string) string {
				source = withFmt(source)

				return strings.Replace(source, `req = req.WithContext(ctx)`,
					`req = req.WithContext(ctx)
	req.URL.Path = fmt.Sprintf("/prefix/%s/extra", req.URL.Path)`, 1)
			},
		},
		{
			name: "invalid format string changes path",
			mutate: func(source string) string {
				source = withFmt(source)

				return strings.Replace(source, `req = req.WithContext(ctx)`,
					`req = req.WithContext(ctx)
	req.URL.Path = fmt.Sprintf("/%s/%s", req.URL.Path)`, 1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertRejectedRouteMutation(t, test.mutate(generatedRequestSource))
		})
	}
}

func TestGeneratedRequestRejectsUnmodeledHeaderWritesAndAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change string
	}{
		{
			name:   "direct header setter",
			change: `req.Header.Set("X-Unmodeled", "value")`,
		},
		{
			name: "header setter method value",
			change: `setHeader := req.Header.Set
	setHeader("X-Unmodeled", "value")`,
		},
		{
			name:   "converted header setter",
			change: `http.Header(req.Header).Set("X-Unmodeled", "value")`,
		},
		{
			name: "header map alias write",
			change: `headers := req.Header
	headers["X-Unmodeled"] = []string{"value"}`,
		},
		{
			name:   "header map escapes to unverified helper",
			change: `c.inspectHeaders(req.Header)`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := strings.Replace(generatedRequestSource, `req = req.WithContext(ctx)`,
				`req = req.WithContext(ctx)
	`+test.change, 1)
			assertRejectedRouteMutation(t, source)
		})
	}
}

func TestGeneratedRequestRejectsQueryMapAndMethodValueAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change string
	}{
		{
			name: "query setter method value",
			change: `setQuery := req.URL.Query().Set
	setQuery("unmodeled", "value")`,
		},
		{
			name: "query map alias write",
			change: `query := req.URL.Query()
		query["unmodeled"] = []string{"value"}
		req.URL.RawQuery = query.Encode()`,
		},
		{
			name:   "query map escapes to unverified helper",
			change: `c.inspectQuery(req.URL.Query())`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := strings.Replace(generatedRequestSource, `req = req.WithContext(ctx)`,
				`req = req.WithContext(ctx)
	`+test.change, 1)
			assertRejectedRouteMutation(t, source)
		})
	}
}

func TestGeneratedQueryAndHeaderKeysAreRequiredAcrossAliasesAndMaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change string
	}{
		{
			name: "raw query key in url.Values literal",
			change: `params := url.Values{"unmodeled": []string{"value"}}
	req.URL.RawQuery = params.Encode()`,
		},
		{
			name: "generated query map aliased setter adds raw key",
			change: `params := url.Values{routes.QueryParamPage: []string{"value"}}
		setQuery := params.Set
		setQuery("unmodeled", "value")
		req.URL.RawQuery = params.Encode()`,
		},
		{
			name: "parenthesized query receiver adds raw key",
			change: `params := url.Values{routes.QueryParamPage: []string{"value"}}
		(params).Set("unmodeled", "value")
		req.URL.RawQuery = params.Encode()`,
		},
		{
			name: "parenthesized query map index adds raw key",
			change: `params := url.Values{routes.QueryParamPage: []string{"value"}}
		(params)["unmodeled"] = []string{"value"}
		req.URL.RawQuery = params.Encode()`,
		},
		{
			name: "URL.Query result is not a generated-key source",
			change: `params := req.URL.Query()
		params.Set(routes.QueryParamPage, "value")
		req.URL.RawQuery = params.Encode()`,
		},
		{
			name: "generated query map escapes to unverified helper",
			change: `params := url.Values{routes.QueryParamPage: []string{"value"}}
	c.inspectQuery(params)`,
		},
		{
			name:   "raw request header indexed write",
			change: `req.Header["X-Unmodeled"] = []string{"value"}`,
		},
		{
			name:   "parenthesized request header indexed write",
			change: `(req.Header)["X-Unmodeled"] = []string{"value"}`,
		},
		{
			name:   "raw request header map replacement",
			change: `req.Header = http.Header{"X-Unmodeled": []string{"value"}}`,
		},
		{
			name: "generated header alias adds raw key",
			change: `headers := http.Header(req.Header)
		setHeader := headers.Set
		setHeader("X-Unmodeled", "value")`,
		},
		{
			name: "generated custom-header map escapes to unverified helper",
			change: `headers := http.Header{routes.HeaderXMode: []string{"value"}}
		c.inspectHeaders(headers)`,
		},
		{
			name: "generated query map embedded in an aggregate escapes",
			change: `aggregate := struct { values url.Values }{
			values: url.Values{routes.QueryParamPage: []string{"value"}},
		}
		c.inspectAggregate(aggregate)`,
		},
		{
			name: "generated header map embedded in an aggregate escapes",
			change: `aggregate := struct { headers http.Header }{
				headers: http.Header{routes.HeaderXMode: []string{"value"}},
			}
		c.inspectAggregate(aggregate)`,
		},
		{
			name: "generated header map stored in a field loses trust",
			change: `holder := struct { headers http.Header }{
				headers: http.Header{routes.HeaderXMode: []string{"value"}},
			}
		holder.headers["X-Unmodeled"] = []string{"value"}`,
		},
		{
			name: "generated header map stored in an index loses trust",
			change: `holders := []http.Header{
				http.Header{routes.HeaderXMode: []string{"value"}},
			}
			holders[0]["X-Unmodeled"] = []string{"value"}`,
		},
		{
			name: "generated query map stored in a field loses trust",
			change: `holder := struct { values url.Values }{
			values: url.Values{routes.QueryParamPage: []string{"value"}},
		}
		holder.values.Set("unmodeled", "value")`,
		},
		{
			name: "generated query map stored in an index loses trust",
			change: `holders := []url.Values{
				url.Values{routes.QueryParamPage: []string{"value"}},
			}
		holders[0]["unmodeled"] = []string{"value"}`,
		},
		{
			name:   "custom header map returned by unverified helper",
			change: `req.Header = c.unverifiedHeaderFactory()` + "\n",
		},
		{
			name: "inner generated-key map does not hide outer raw-key map",
			change: `headers := map[string][]string{"X-Unmodeled": []string{"outer"}}
		{
			headers := map[string][]string{routes.HeaderXMode: []string{"inner"}}
			_ = headers
		}
		for key, values := range headers {
			req.Header[key] = values
		}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			source := withURLImport(generatedRequestSource)

			source = strings.Replace(source, `req = req.WithContext(ctx)`,
				`req = req.WithContext(ctx)
	`+test.change, 1)

			if strings.Contains(test.change, "unverifiedHeaderFactory") {
				source += `
func (c *API) unverifiedHeaderFactory() http.Header {
	return http.Header{routes.HeaderXMode: []string{"value"}}
}
`
			}

			findings := audit(t, fixtureRootWithQueryAndHeaderParams(t, source))
			if len(findings) == 0 {
				t.Fatal("route gate accepted an unmodeled query/header key or escaped schema-keyed map")
			}
		})
	}
}

func TestGeneratedQueryAndHeaderKeysSurviveSafeAliases(t *testing.T) {
	t.Parallel()

	source := withURLImport(generatedRequestSource)
	source = strings.Replace(source, `req = req.WithContext(ctx)`, `
	params := url.Values{routes.QueryParamPage: []string{"1"}}
	params.Set(routes.QueryParamPage, "2")
	req.URL.RawQuery = params.Encode()
	req.Header.Set(routes.HeaderXMode, "compact")
	requestHeaders := http.Header(req.Header)
	requestHeaders.Set(routes.HeaderXMode, "aliased")
	customHeaders := http.Header{routes.HeaderXMode: []string{"draft"}}
	customHeaders.Set(routes.HeaderXMode, "compact")
	req.Header = customHeaders
	req = req.WithContext(ctx)`, 1)

	findings := audit(t, fixtureRootWithQueryAndHeaderParams(t, source))
	if len(findings) != 0 {
		t.Fatalf("generated schema keys lost trust through a local alias:\n%s", strings.Join(findings, "\n"))
	}
}

func TestGeneratedMapsCannotEscapeToCrossFunctionState(t *testing.T) {
	t.Parallel()

	t.Run("query map assigned to package variable", func(t *testing.T) {
		t.Parallel()

		source := withURLImport(generatedRequestSource)
		source = strings.Replace(source, `type API struct { httpClient *http.Client }`,
			`var savedQuery url.Values

type API struct { httpClient *http.Client }`, 1)
		source = strings.Replace(source, `req = req.WithContext(ctx)`,
			`params := url.Values{routes.QueryParamPage: []string{"value"}}
	savedQuery = params
	req = req.WithContext(ctx)`, 1)
		assertRejectedParameterizedRouteMutation(t, source)
	})

	t.Run("header map assigned to package variable", func(t *testing.T) {
		t.Parallel()

		source := strings.Replace(generatedRequestSource, `type API struct { httpClient *http.Client }`,
			`var savedHeaders http.Header

type API struct { httpClient *http.Client }`, 1)
		source = strings.Replace(source, `req = req.WithContext(ctx)`,
			`headers := http.Header{routes.HeaderXMode: []string{"value"}}
	savedHeaders = headers
	req = req.WithContext(ctx)`, 1)
		assertRejectedParameterizedRouteMutation(t, source)
	})
}

func TestGeneratedRouteTrustDoesNotCrossLexicalOrConditionalBindings(t *testing.T) {
	t.Parallel()

	t.Run("same named request in nested block", func(t *testing.T) {
		t.Parallel()

		source := strings.Replace(generatedRequestSource, `req = req.WithContext(ctx)`, `if true {
		req := &http.Request{}
		_, err = c.httpClient.Do(req)
		return err
	}
	req = req.WithContext(ctx)`, 1)
		assertRejectedRouteMutation(t, source)
	})

	t.Run("outer request parameter assigned generated value on one branch", func(t *testing.T) {
		t.Parallel()

		source := generatedRequestSource + `
func (c *API) MaybeGetWidget(ctx context.Context, req *http.Request, widgetID string, useGenerated bool) error {
	var err error
	if useGenerated {
		req, err = routes.NewGetWidgetRequest("https://api.example.test", widgetID)
		if err != nil { return err }
	}
	_, err = c.httpClient.Do(req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})
}

func TestNetworkMethodAliasesRemainVisibleAtPackageAndFunctionScope(t *testing.T) {
	t.Parallel()

	t.Run("file scope method value of injected client field", func(t *testing.T) {
		t.Parallel()

		source := strings.Replace(generatedRequestSource,
			`type API struct { httpClient *http.Client }`,
			`type API struct { httpClient *http.Client }

var sharedClient = &http.Client{}
var sharedDo = sharedClient.Do`, 1)
		source += `
func (c *API) SendThroughSharedDo(req *http.Request) error {
	_, err := sharedDo(req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})

	t.Run("local method value of injected client field", func(t *testing.T) {
		t.Parallel()

		source := generatedRequestSource + `
func (c *API) SendThroughLocalDo(req *http.Request) error {
	do := c.httpClient.Do
	_, err := do(req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})

	t.Run("package scope method expression", func(t *testing.T) {
		t.Parallel()

		source := strings.Replace(generatedRequestSource,
			`type API struct { httpClient *http.Client }`,
			`type API struct { httpClient *http.Client }

var sharedDo = http.Client.Do`, 1)
		source += `
func (c *API) SendThroughMethodExpression(req *http.Request) error {
	_, err := sharedDo(c.httpClient, req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})

	t.Run("local method value of trusted transport helper", func(t *testing.T) {
		t.Parallel()

		source := generatedRequestSource + `
func (c *API) SendThroughLocalRetry(ctx context.Context, req *http.Request) error {
	retry := c.sendWithRetry
	_, err := retry(ctx, req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})

	t.Run("package scope method expression of trusted transport helper", func(t *testing.T) {
		t.Parallel()

		source := strings.Replace(generatedRequestSource,
			`type API struct { httpClient *http.Client }`,
			`type API struct { httpClient *http.Client }

var sharedRetry = (*API).sendWithRetry`, 1)
		source += `
func (c *API) SendThroughSharedRetry(ctx context.Context, req *http.Request) error {
	_, err := sharedRetry(c, ctx, req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})

	t.Run("local RoundTrip alias after receiver assignment", func(t *testing.T) {
		t.Parallel()

		source := generatedRequestSource + `
func (c *API) SendThroughLocalRoundTrip(req *http.Request) error {
	transport := http.DefaultTransport
	roundTrip := transport.RoundTrip
	_, err := roundTrip(req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})

	t.Run("package scope RoundTrip alias after receiver assignment", func(t *testing.T) {
		t.Parallel()

		source := strings.Replace(generatedRequestSource,
			`type API struct { httpClient *http.Client }`,
			`type API struct { httpClient *http.Client }

var sharedTransport = http.DefaultTransport
var sharedRoundTrip = sharedTransport.RoundTrip`, 1)
		source += `
func (c *API) SendThroughSharedRoundTrip(req *http.Request) error {
	_, err := sharedRoundTrip(req)
	return err
}
`
		assertRejectedRouteMutation(t, source)
	})
}

func assertRejectedRouteMutation(t *testing.T, source string) {
	t.Helper()

	findings := audit(t, fixtureRoot(t, source))
	if len(findings) == 0 {
		t.Fatal("route gate accepted an adversarial route or network mutation")
	}
}

func assertRejectedParameterizedRouteMutation(t *testing.T, source string) {
	t.Helper()

	findings := audit(t, fixtureRootWithQueryAndHeaderParams(t, source))
	if len(findings) == 0 {
		t.Fatal("route gate accepted an escaped, reassigned, or stored schema-keyed map")
	}
}

func withURLImport(source string) string {
	return strings.Replace(source, "import (", "import (\n\t\"net/url\"", 1)
}

func fixtureRootWithQueryAndHeaderParams(t *testing.T, source string) string {
	t.Helper()
	root := fixtureRoot(t, source)

	openAPIPath := filepath.Join(root, "api", "openapi.yaml")

	openAPI, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatal(err)
	}

	pathParameter := `        - {name: widget_id, in: path, required: true, schema: {type: string}}`
	pathAndWireParameters := pathParameter + `
        - {name: page, in: query, required: false, schema: {type: string}}
        - {name: X-Mode, in: header, required: false, schema: {type: string}}`

	updatedOpenAPI := strings.Replace(string(openAPI), pathParameter, pathAndWireParameters, 1)
	if updatedOpenAPI == string(openAPI) {
		t.Fatal("fixture path parameter was not found")
	}

	err = os.WriteFile(openAPIPath, []byte(updatedOpenAPI), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	generatedPath := filepath.Join(root, "generatedhttp", "client.gen.go")

	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}

	generated = append(generated, []byte(`
const QueryParamPage = "page"
const HeaderXMode = "X-Mode"
`)...)

	err = os.WriteFile(generatedPath, generated, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	return root
}
