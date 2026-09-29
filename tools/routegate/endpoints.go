package routegate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var routeParameterPattern = regexp.MustCompile(`\{[^}]+\}`)
var routePlaceholderPattern = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_]*)\}`)

func auditProtocolEndpoints(root string, contracts Contracts) []Finding {
	path := filepath.Join(root, "internal", "protocol", "endpoints.go")
	fileSet := token.NewFileSet()

	file, err := parser.ParseFile(fileSet, path, nil, 0)
	if err != nil {
		return []Finding{{
			Path:    "internal/protocol/endpoints.go",
			Line:    0,
			Rule:    "endpoint-inventory",
			Message: fmt.Sprintf("cannot parse endpoint inventory: %v", err),
		}}
	}

	var findings []Finding

	for _, declaration := range file.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok || group.Tok != token.CONST {
			continue
		}

		for _, specification := range group.Specs {
			values, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for index, name := range values.Names {
				if index >= len(values.Values) {
					continue
				}

				findings = append(findings, auditEndpointConstant(fileSet, name, values.Values[index], contracts)...)
			}
		}
	}

	return findings
}

func auditEndpointConstant(
	fileSet *token.FileSet,
	name *ast.Ident,
	expression ast.Expr,
	contracts Contracts,
) []Finding {
	value, ok := stringConstant(expression)
	if !ok {
		return nil
	}

	add := func(rule, message string) []Finding {
		return []Finding{{
			Path:    "internal/protocol/endpoints.go",
			Line:    fileSet.Position(name.Pos()).Line,
			Rule:    rule,
			Message: fmt.Sprintf("%s: %s", name.Name, message),
		}}
	}

	if strings.HasPrefix(value, "/") {
		if !schemaHasPath(value, contracts) {
			message := fmt.Sprintf(
				"path %q is not covered by an OpenAPI route or AsyncAPI channel",
				value,
			)

			return add("unmodeled-endpoint-path", message)
		}

		return nil
	}

	if !strings.Contains(value, "://") || name.Name == "OAuthCallbackURL" {
		return nil
	}

	target, err := url.Parse(value)
	if err != nil || target.Host == "" {
		return add("invalid-endpoint-url", "URL constant cannot be parsed into an authority")
	}

	if target.Scheme == "http" || target.Scheme == "https" {
		return auditHTTPBaseURL(name, target, contracts, add)
	}

	if target.Scheme != "ws" && target.Scheme != "wss" {
		return nil
	}

	return auditWebSocketBaseURL(target, contracts, add)
}

func auditHTTPBaseURL(
	name *ast.Ident,
	target *url.URL,
	contracts Contracts,
	add func(string, string) []Finding,
) []Finding {
	var findings []Finding

	origin := canonicalOrigin(target)

	if !hasMatchingOrigin(contracts.HTTPServers, origin) {
		message := fmt.Sprintf("origin %q is absent from OpenAPI servers", origin)
		findings = append(findings, add("unmodeled-endpoint-origin", message)...)
	}

	baseURL := name.Name == "APIBaseURL" || name.Name == "OAuthBaseURL" || name.Name == "USSolutionsBaseURL"

	if !baseURL && target.Path != "" && target.Path != "/" {
		message := fmt.Sprintf("base path %q is not an inventoried server prefix", target.Path)
		findings = append(findings, add("unmodeled-endpoint-prefix", message)...)
	}

	return findings
}

func auditWebSocketBaseURL(target *url.URL, contracts Contracts, add func(string, string) []Finding) []Finding {
	channel, found := channelForTarget(target, contracts)
	if !found {
		message := fmt.Sprintf(
			"%s://%s%s is not an AsyncAPI server/channel pair",
			target.Scheme,
			target.Host,
			target.Path,
		)

		return add("unmodeled-websocket-endpoint", message)
	}

	if message := validateWebSocketQuery(target.RawQuery, channel); message != "" {
		return add("unmodeled-websocket-query", message)
	}

	return nil
}

func validateWebSocketQuery(rawQuery string, channel SignalingChannel) string {
	if !channel.HasQuery {
		if rawQuery != "" {
			return fmt.Sprintf("channel %q has no AsyncAPI query binding but URL includes %q", channel.Address, rawQuery)
		}

		return ""
	}

	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return fmt.Sprintf("query %q is malformed: %v", rawQuery, err)
	}

	for _, key := range channel.Query.Required {
		if len(values[key]) == 0 {
			return fmt.Sprintf("required query key %q is missing from channel %q", key, channel.Address)
		}
	}

	for key, entries := range values {
		property, exists := channel.Query.Properties[key]
		if !exists {
			if !channel.Query.AdditionalProperties {
				return fmt.Sprintf("query key %q is not allowed by channel %q", key, channel.Address)
			}

			continue
		}

		if len(entries) != 1 {
			return fmt.Sprintf("query key %q occurs %d times; AsyncAPI requires a single value", key, len(entries))
		}

		if message := validateQueryValue(key, entries[0], property, channel.Address); message != "" {
			return message
		}
	}

	return ""
}

func validateQueryValue(key, value string, property AsyncQueryProperty, channelAddress string) string {
	placeholders := routePlaceholderPattern.FindAllStringSubmatch(value, -1)
	if len(placeholders) > 1 {
		return fmt.Sprintf("query key %q in channel %q has multiple dynamic placeholders", key, channelAddress)
	}

	if len(placeholders) == 1 && placeholders[0][1] != key {
		return fmt.Sprintf("query key %q uses placeholder %q instead of its own name", key, placeholders[0][1])
	}

	if len(placeholders) != 0 && (property.Const != "" || len(property.Enum) != 0) {
		return fmt.Sprintf("query key %q cannot use a placeholder with a const or enum constraint", key)
	}

	valueForValidation := routePlaceholderPattern.ReplaceAllString(value, "x")
	if property.Const != "" && valueForValidation != property.Const {
		return fmt.Sprintf("query key %q has value %q; AsyncAPI requires %q", key, value, property.Const)
	}

	if len(property.Enum) != 0 && !contains(property.Enum, valueForValidation) {
		return fmt.Sprintf("query key %q has value %q outside its AsyncAPI enum", key, value)
	}

	if property.MinLength > 0 && len([]rune(valueForValidation)) < property.MinLength {
		return fmt.Sprintf("query key %q is shorter than the AsyncAPI minLength %d", key, property.MinLength)
	}

	if property.Pattern != "" {
		pattern, err := regexp.Compile(property.Pattern)
		if err != nil {
			return fmt.Sprintf("channel %q has invalid query pattern for %q: %v", channelAddress, key, err)
		}

		if !pattern.MatchString(valueForValidation) {
			return fmt.Sprintf("query key %q has value %q outside its AsyncAPI pattern", key, value)
		}
	}

	return ""
}

func stringConstant(expression ast.Expr) (string, bool) {
	switch value := expression.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}

		text, err := strconv.Unquote(value.Value)

		return text, err == nil
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}

		left, leftOK := stringConstant(value.X)
		right, rightOK := stringConstant(value.Y)

		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}

func schemaHasPath(path string, contracts Contracts) bool {
	normalized := normalizeRoutePath(path)
	for _, route := range contracts.HTTPRoutes {
		if normalizeRoutePath(route.Path) == normalized {
			return true
		}
	}

	for _, channel := range contracts.SignalingChannels {
		if normalizeRoutePath(channel.Address) == normalized {
			return true
		}
	}

	return false
}

func normalizeRoutePath(path string) string {
	return routeParameterPattern.ReplaceAllString(path, "{}")
}

func canonicalOrigin(parsed *url.URL) string {
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())

	port := parsed.Port()

	httpsDefault := (scheme == "https" || scheme == "wss") && port == "443"
	httpDefault := (scheme == "http" || scheme == "ws") && port == "80"

	if port != "" && !httpsDefault && !httpDefault {
		host += ":" + port
	}

	return scheme + "://" + host
}

func hasMatchingOrigin(servers map[string]struct{}, expected string) bool {
	for server := range servers {
		parsed, err := url.Parse(server)
		if err == nil && canonicalOrigin(parsed) == expected {
			return true
		}
	}

	return false
}

func channelForTarget(target *url.URL, contracts Contracts) (SignalingChannel, bool) {
	origin := canonicalOrigin(target)

	for _, channel := range contracts.SignalingChannels {
		server, exists := contracts.SignalingServers[channel.Server]
		if !exists || normalizeRoutePath(channel.Address) != normalizeRoutePath(target.Path) {
			continue
		}

		parsed, err := url.Parse(server)
		if err == nil && canonicalOrigin(parsed) == origin {
			return channel, true
		}
	}

	return SignalingChannel{
		Address:  "",
		Server:   "",
		Query:    AsyncQuery{Required: nil, Properties: nil, AdditionalProperties: false},
		HasQuery: false,
	}, false
}
