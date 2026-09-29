package protocol

import (
	"net/url"
	"strings"
)

// ValidateWebSocketURL checks a default endpoint against its AsyncAPI channel.
func ValidateWebSocketURL(channel, rawURL string) error {
	parsed, err := parseWebSocketURL(rawURL)
	if err != nil {
		return err
	}

	switch channel {
	case SignalingChannel:
		if parsed.Scheme != signalingProtocol || parsed.Hostname() != signalingHost ||
			!webSocketPortMatches(parsed.Port(), signalingPort) || parsed.EscapedPath() != signalingPath {
			return webSocketURLError{reason: reasonSignalingEndpoint, cause: nil}
		}

		return validateSignalingQuery(parsed.Query())
	case AccountEventsChannel:
		if parsed.Scheme != accountEventsProtocol || parsed.Hostname() != accountEventsHost ||
			!webSocketPortMatches(parsed.Port(), accountEventsPort) || parsed.EscapedPath() != accountEventsPath ||
			parsed.RawQuery != "" {
			return webSocketURLError{reason: reasonEventEndpoint, cause: nil}
		}

		return nil
	default:
		return webSocketURLError{reason: reasonUnknownChannel, cause: nil}
	}
}

func webSocketPortMatches(actual, expected string) bool {
	return actual == "" || actual == expected
}

// ValidateWebSocketOverride checks a caller-supplied transport URL.
func ValidateWebSocketOverride(rawURL string) error {
	_, err := parseWebSocketURL(rawURL)

	return err
}

func parseWebSocketURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, webSocketURLError{reason: reasonInvalidURL, cause: err}
	}

	if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" ||
		(parsed.Scheme != "ws" && parsed.Scheme != "wss") {
		return nil, webSocketURLError{reason: reasonInvalidURL, cause: nil}
	}

	return parsed, nil
}

func validateSignalingQuery(values url.Values) error {
	queryKeys := signalingQueryKeys()
	if len(values) != len(queryKeys) {
		return webSocketURLError{reason: reasonUnexpectedQuery, cause: nil}
	}

	for _, name := range queryKeys {
		if len(values[name]) != 1 {
			return webSocketURLError{reason: reasonMissingQueryValue, cause: nil}
		}
	}

	clientID := values.Get(signalingQueryClientIdKey)
	if values.Get(signalingQueryApiVersionKey) != signalingApiVersion ||
		values.Get(signalingQueryAuthTypeKey) != signalingAuthType ||
		!strings.HasPrefix(clientID, signalingClientIdPrefix) ||
		len(clientID) <= len(signalingClientIdPrefix) || values.Get(signalingQueryTokenKey) == "" {
		return webSocketURLError{reason: reasonSignalingQuery, cause: nil}
	}

	return nil
}
