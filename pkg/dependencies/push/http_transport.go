package push

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	checkinpb "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/checkin"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type diagnosticTransport struct {
	failed atomic.Value
	next   http.RoundTripper
}

// NewRegistrationTransport validates every outbound FCM HTTP route and applies
// the known registration compatibility transformations before forwarding it.
func NewRegistrationTransport(next http.RoundTripper) http.RoundTripper {
	return newDiagnosticTransport(next)
}

func newDiagnosticTransport(next http.RoundTripper) *diagnosticTransport {
	if next == nil {
		next = http.DefaultTransport
	}

	return &diagnosticTransport{failed: atomic.Value{}, next: next}
}

type fcmHTTPRoute string

const (
	routeCheckin       fcmHTTPRoute = "checkin"
	routeRegister      fcmHTTPRoute = "register"
	routeInstallations fcmHTTPRoute = "installations"
	routeRegistrations fcmHTTPRoute = "registrations"

	fcmFIDByteCount               = 17
	fcmWebPushP256DHByteCount     = 65
	fcmWebPushAuthSecretByteCount = 16
	fcmRegistrationWebFieldCount  = 3
)

func (t *diagnosticTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	route, err := matchFCMRoute(request)
	if err != nil {
		return nil, err
	}

	err = validateFCMHeaders(route, request)
	if err != nil {
		return nil, err
	}

	if route == routeRegistrations {
		err = omitDefaultVAPID(request)
		if err != nil {
			return nil, err
		}
	}

	body, err := readAndRestoreBody(request)
	if err != nil {
		return nil, err
	}

	err = validateFCMRequestBody(route, body)
	if err != nil {
		return nil, err
	}

	response, err := t.next.RoundTrip(request)
	if response != nil && response.StatusCode >= http.StatusBadRequest {
		t.failed.Store(request.URL.Host + request.URL.EscapedPath())
	} else if err == nil {
		t.failed.Store("")
	}

	if err != nil {
		return response, ringerrors.NewNetworkError("FCM HTTP request failed", err)
	}

	return response, nil
}

func matchFCMRoute(request *http.Request) (fcmHTTPRoute, error) {
	if request == nil || request.URL == nil ||
		request.URL.Scheme != "https" || request.URL.User != nil ||
		request.URL.RawQuery != "" || request.URL.ForceQuery || request.URL.Fragment != "" || request.URL.Opaque != "" ||
		(request.Host != "" && request.Host != request.URL.Host) {
		return "", ringerrors.NewNetworkError("unlisted FCM HTTP route", nil)
	}

	if request.Method != http.MethodPost {
		return "", ringerrors.NewNetworkError("unlisted FCM HTTP route", nil)
	}

	switch {
	case request.URL.Host == protocol.FCMCheckinHost && request.URL.EscapedPath() == protocol.FCMCheckinPath:
		return routeCheckin, nil
	case request.URL.Host == protocol.FCMRegisterHost && request.URL.EscapedPath() == protocol.FCMRegisterPath:
		return routeRegister, nil
	case request.URL.Host == protocol.FCMInstallationsHost && request.URL.EscapedPath() == protocol.FCMInstallationsPath:
		return routeInstallations, nil
	case request.URL.Host == protocol.FCMRegistrationsHost && request.URL.EscapedPath() == protocol.FCMRegistrationsPath:
		return routeRegistrations, nil
	default:
		return "", ringerrors.NewNetworkError("unlisted FCM HTTP route", nil)
	}
}

func validateFCMHeaders(route fcmHTTPRoute, request *http.Request) error {
	var expected http.Header

	switch route {
	case routeCheckin:
		expected = http.Header{protocol.FCMContentTypeHeader: {protocol.FCMContentTypeProtobuf}}
	case routeRegister:
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, "AidLogin ") {
			return invalidFCMHeaderError()
		}

		identifier, secret, ok := strings.Cut(strings.TrimPrefix(authorization, "AidLogin "), ":")
		if !ok || !decimal(identifier) || !decimal(secret) {
			return invalidFCMHeaderError()
		}

		expected = http.Header{
			protocol.FCMContentTypeHeader: {protocol.FCMContentTypeForm},
			"Authorization":               {authorization},
			"User-Agent":                  {""},
		}
	case routeInstallations:
		expected = http.Header{
			protocol.FCMContentTypeHeader:         {protocol.FCMContentTypeJSON},
			protocol.FCMAcceptHeader:              {protocol.FCMContentTypeJSON},
			protocol.FCMInstallationsAPIKeyHeader: {apiKey},
		}
	case routeRegistrations:
		authorization := request.Header.Get(protocol.FCMInstallationsAuthHeader)

		token := strings.TrimPrefix(authorization, protocol.FCMInstallationsAuthPrefix)
		if strings.TrimSpace(token) == "" {
			return invalidFCMHeaderError()
		}

		expected = http.Header{
			protocol.FCMContentTypeHeader:         {protocol.FCMContentTypeJSON},
			protocol.FCMInstallationsAPIKeyHeader: {apiKey},
			protocol.FCMInstallationsAuthHeader:   {authorization},
		}
	default:
		return ringerrors.NewNetworkError("unlisted FCM HTTP route", nil)
	}

	if !sameHeaderSet(expected, request.Header) {
		return invalidFCMHeaderError()
	}

	return nil
}

func sameHeaderSet(expected, actual http.Header) bool {
	if len(expected) != len(actual) {
		return false
	}

	for name, values := range expected {
		actualValues := actual.Values(name)
		if len(values) != len(actualValues) {
			return false
		}

		for index, value := range values {
			if actualValues[index] != value {
				return false
			}
		}
	}

	return true
}

func invalidFCMHeaderError() error {
	return ringerrors.NewBadRequestError("FCM request headers do not match the pinned protocol", nil)
}

func decimal(value string) bool {
	if value == "" {
		return false
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}

	return true
}

func readAndRestoreBody(request *http.Request) ([]byte, error) {
	if request.Body == nil {
		return nil, ringerrors.NewBadRequestError("FCM request body is required", nil)
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("read FCM request body", err)
	}

	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))

	return body, nil
}

func validateFCMRequestBody(route fcmHTTPRoute, body []byte) error {
	switch route {
	case routeCheckin:
		var request checkinpb.AndroidCheckinRequest

		err := proto.Unmarshal(body, &request)
		if err != nil {
			return ringerrors.NewBadRequestError("decode FCM check-in request", err)
		}

		return validateCheckinRequest(&request)
	case routeRegister:
		return validateRegisterRequest(body)
	case routeInstallations:
		return validateInstallationsRequest(body)
	case routeRegistrations:
		return validateRegistrationsRequest(body)
	default:
		return ringerrors.NewNetworkError("unlisted FCM HTTP route", nil)
	}
}

func validateCheckinRequest(request *checkinpb.AndroidCheckinRequest) error {
	if request.GetVersion() != 3 || request.GetFragment() != 0 || request.GetUserSerialNumber() != 0 ||
		request.GetCheckin() == nil || request.GetCheckin().GetUserNumber() != 0 ||
		request.GetCheckin().GetType() != checkinpb.DeviceType_DEVICE_CHROME_BROWSER ||
		request.GetCheckin().GetChromeBuild() == nil ||
		request.GetCheckin().GetChromeBuild().GetPlatform() != checkinpb.ChromeBuildProto_PLATFORM_LINUX ||
		request.GetCheckin().GetChromeBuild().GetChromeVersion() != "63.0.3234.0" ||
		request.GetCheckin().GetChromeBuild().GetChannel() != checkinpb.ChromeBuildProto_CHANNEL_STABLE ||
		!exactProtoFields(request, "id", "security_token", "checkin", "version", "fragment", "user_serial_number") ||
		!exactProtoFields(request.GetCheckin(), "chrome_build", "type", "user_number") ||
		!exactProtoFields(request.GetCheckin().GetChromeBuild(), "platform", "chrome_version", "channel") {
		return ringerrors.NewBadRequestError("FCM check-in request violates its pinned protobuf contract", nil)
	}

	if len(request.ProtoReflect().GetUnknown()) > 0 || len(request.GetCheckin().ProtoReflect().GetUnknown()) > 0 ||
		len(request.GetCheckin().GetChromeBuild().ProtoReflect().GetUnknown()) > 0 {
		return ringerrors.NewBadRequestError("FCM check-in request contains unknown fields", nil)
	}

	return nil
}

func exactProtoFields(message proto.Message, names ...protoreflect.Name) bool {
	wanted := make(map[protoreflect.Name]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}

	valid := true

	message.ProtoReflect().Range(func(field protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		if _, ok := wanted[field.Name()]; !ok {
			valid = false

			return false
		}

		delete(wanted, field.Name())

		return true
	})

	return valid && len(wanted) == 0
}

func validateRegisterRequest(body []byte) error {
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return ringerrors.NewBadRequestError("decode FCM legacy registration request", err)
	}

	if len(values) != 4 ||
		!oneValueIs(values, protocol.FCMRegisterFormAppKey, protocol.FCMAndroidAppIdentifier) ||
		!oneValueIs(values, protocol.FCMRegisterFormSubtypeKey, appID) ||
		!oneValueIs(values, protocol.FCMRegisterFormDeviceKey, values.Get(protocol.FCMRegisterFormDeviceKey)) ||
		!decimal(values.Get(protocol.FCMRegisterFormDeviceKey)) ||
		!oneValueIs(values, protocol.FCMRegisterFormSenderKey, protocol.FCMDefaultVAPIDKey) {
		return ringerrors.NewBadRequestError("FCM legacy registration request violates its pinned form contract", nil)
	}

	return nil
}

func validateInstallationsRequest(body []byte) error {
	fields, err := jsonObject(body)
	if err != nil {
		return ringerrors.NewBadRequestError("decode Firebase installations request", err)
	}

	if !exactJSONKeys(fields, protocol.FCMInstallationsAppIDKey, protocol.FCMInstallationsAuthVersionKey,
		protocol.FCMInstallationsFIDKey, protocol.FCMInstallationsSDKVersionKey) ||
		!jsonStringIs(fields, protocol.FCMInstallationsAppIDKey, appID) ||
		!jsonStringIs(fields, protocol.FCMInstallationsAuthVersionKey, protocol.FCMInstallationsAuthVersion) ||
		!validFID(jsonString(fields, protocol.FCMInstallationsFIDKey)) ||
		!jsonStringIs(fields, protocol.FCMInstallationsSDKVersionKey, protocol.FCMInstallationsSDKVersion) {
		return ringerrors.NewBadRequestError("Firebase installations request violates its pinned JSON contract", nil)
	}

	return nil
}

func validateRegistrationsRequest(body []byte) error {
	fields, err := jsonObject(body)
	if err != nil {
		return ringerrors.NewBadRequestError("decode FCM registrations request", err)
	}

	if !exactJSONKeys(fields, protocol.FCMRegistrationWebKey) {
		return ringerrors.NewBadRequestError("FCM registrations request violates its pinned JSON contract", nil)
	}

	webFields, err := jsonObject(fields[protocol.FCMRegistrationWebKey])
	if err != nil {
		return ringerrors.NewBadRequestError("decode FCM registrations web request", err)
	}

	endpoint := jsonString(webFields, protocol.FCMRegistrationEndpointKey)
	if !exactJSONKeys(webFields, protocol.FCMRegistrationEndpointKey, protocol.FCMRegistrationP256DHKey,
		protocol.FCMRegistrationAuthKey) || !strings.HasPrefix(endpoint, protocol.FCMRegistrationEndpointPrefix) ||
		strings.TrimPrefix(endpoint, protocol.FCMRegistrationEndpointPrefix) == "" ||
		!validWebPushKey(jsonString(webFields, protocol.FCMRegistrationP256DHKey), fcmWebPushP256DHByteCount) ||
		!validWebPushKey(jsonString(webFields, protocol.FCMRegistrationAuthKey), fcmWebPushAuthSecretByteCount) {
		return ringerrors.NewBadRequestError("FCM registrations request violates its pinned JSON contract", nil)
	}

	return nil
}

func oneValueIs(values url.Values, name, want string) bool {
	actual := values[name]

	return len(actual) == 1 && actual[0] == want
}

func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage

	err := json.Unmarshal(data, &fields)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("decode FCM JSON request", err)
	}

	if fields == nil {
		return nil, ringerrors.NewBadRequestError("FCM JSON request must be an object", nil)
	}

	return fields, nil
}

func exactJSONKeys(fields map[string]json.RawMessage, names ...string) bool {
	if len(fields) != len(names) {
		return false
	}

	for _, name := range names {
		if _, ok := fields[name]; !ok {
			return false
		}
	}

	return true
}

func jsonString(fields map[string]json.RawMessage, name string) string {
	var value string
	if json.Unmarshal(fields[name], &value) != nil {
		return ""
	}

	return value
}

func jsonStringIs(fields map[string]json.RawMessage, name, want string) bool {
	return jsonString(fields, name) == want
}

func validFID(value string) bool {
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)

	return err == nil && len(decoded) == fcmFIDByteCount && decoded[0]&0xf0 == 0x70
}

func validWebPushKey(value string, wantLength int) bool {
	decoded, err := base64.URLEncoding.Strict().DecodeString(value)

	return err == nil && len(decoded) == wantLength
}

// normalizeRegistrationCompatibility accepts the pinned receiver's legacy
// request shape and normalizes it to the generated external operation model.
// The generated caller already sends that normalized shape.
func omitDefaultVAPID(request *http.Request) error {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return ringerrors.NewBadRequestError("read FCM registration request", err)
	}

	var fields map[string]json.RawMessage

	err = json.Unmarshal(body, &fields)
	if err != nil {
		return ringerrors.NewBadRequestError("decode FCM registration request", err)
	}

	if !exactJSONKeys(fields, protocol.FCMRegistrationWebKey) {
		return ringerrors.NewBadRequestError("FCM registration request violates its pinned JSON contract", nil)
	}

	var web map[string]json.RawMessage

	err = json.Unmarshal(fields[protocol.FCMRegistrationWebKey], &web)
	if err != nil {
		return ringerrors.NewBadRequestError("decode FCM registration web request", err)
	}

	if !exactJSONKeys(web, protocol.FCMRegistrationEndpointKey,
		protocol.FCMRegistrationP256DHKey, protocol.FCMRegistrationAuthKey) {
		if !exactJSONKeys(web, protocol.FCMRegistrationVAPIDKey, protocol.FCMRegistrationEndpointKey,
			protocol.FCMRegistrationP256DHKey, protocol.FCMRegistrationAuthKey) ||
			!jsonStringIs(web, protocol.FCMRegistrationVAPIDKey, protocol.FCMDefaultVAPIDKey) {
			return ringerrors.NewBadRequestError("FCM registration VAPID key does not match its pinned value", nil)
		}

		delete(web, protocol.FCMRegistrationVAPIDKey)
	}

	if len(web) != fcmRegistrationWebFieldCount {
		return ringerrors.NewBadRequestError("FCM registration VAPID key does not match its pinned value", nil)
	}

	if _, legacyShape := web[protocol.FCMRegistrationVAPIDKey]; legacyShape {
		return ringerrors.NewBadRequestError("FCM registration VAPID key was not removed", nil)
	}

	fields[protocol.FCMRegistrationWebKey], err = json.Marshal(web)
	if err != nil {
		return ringerrors.NewBadRequestError("encode FCM registration web request", err)
	}

	body, err = json.Marshal(fields)
	if err != nil {
		return ringerrors.NewBadRequestError("encode FCM registration request", err)
	}

	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))

	authorization := request.Header.Get(protocol.FCMInstallationsAuthHeader)

	token := strings.TrimPrefix(authorization, protocol.FCMInstallationsAuthPrefix)
	if strings.TrimSpace(token) == "" {
		return invalidFCMHeaderError()
	}

	request.Header.Set(protocol.FCMInstallationsAuthHeader, token)

	return nil
}
