package replay_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/generatedhttp"
)

type capturedHTTPExchange struct {
	Response capturedHTTPResponse `json:"response"`
}

type capturedHTTPResponse struct {
	Body json.RawMessage `json:"body"`
}

// A captured field must have a named generated member, rather than surviving
// only in AdditionalProperties. This protects the schema when captures grow.
func TestCapturedDeviceFieldsHaveGeneratedTypes(t *testing.T) {
	paths := []string{"device-detail.json", "device-list.json"}
	for _, pattern := range []string{"device-detail-*.json", "device-list-*.json"} {
		matches, err := filepath.Glob(filepath.Join("fixtures", "http", "captured", "variants", pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			paths = append(paths, filepath.Join("variants", filepath.Base(match)))
		}
	}
	for _, name := range paths {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("fixtures", "http", "captured", name)) // #nosec G304 -- name comes from fixed fixture names or the variants glob above.
			if err != nil {
				t.Fatal(err)
			}
			var exchange capturedHTTPExchange
			if err := json.Unmarshal(data, &exchange); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(filepath.Base(name), "device-detail") {
				var detail generatedhttp.DeviceDetail
				if err := json.Unmarshal(exchange.Response.Body, &detail); err != nil {
					t.Fatal(err)
				}
				assertNoUnmappedDeviceFields(t, reflect.ValueOf(detail.Device), "device")
				return
			}
			var list generatedhttp.DeviceList
			if err := json.Unmarshal(exchange.Response.Body, &list); err != nil {
				t.Fatal(err)
			}
			for _, device := range list.Devices {
				assertNoUnmappedDeviceFields(t, reflect.ValueOf(device), "device")
			}
		})
	}
}

func assertNoUnmappedDeviceFields(t *testing.T, value reflect.Value, path string) {
	t.Helper()
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if field.Name == "AdditionalProperties" {
				if value.Field(i).Len() != 0 {
					t.Errorf("%s has %d captured fields absent from the generated schema", path, value.Field(i).Len())
				}
				continue
			}
			assertNoUnmappedDeviceFields(t, value.Field(i), path+"."+field.Name)
		}
	case reflect.Slice, reflect.Array:
		for i := range value.Len() {
			assertNoUnmappedDeviceFields(t, value.Index(i), path)
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			assertNoUnmappedDeviceFields(t, iter.Value(), path)
		}
	}
}

func TestGeneratedHTTPDeviceHealthFromRecording(t *testing.T) {
	x := deviceListExchange(t, "https://api.ring.com")
	var body generatedhttp.DeviceList
	if err := json.Unmarshal(x.Response.Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Devices) == 0 || body.Devices[0].Health == nil {
		t.Fatal("recorded device health missing")
	}
	health := body.Devices[0].Health
	if health.FirmwareVersion == nil || *health.FirmwareVersion == "" {
		t.Fatal("known health field lost")
	}
	if health.PtzConnected == nil {
		t.Fatal("unobserved hardware health field lost")
	}
}

func TestGeneratedHTTPRecordingFromLegacyFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("fixtures", "http", "baseline", "ring_doorbot_history.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recordings generatedhttp.RecordingArray
	if err := json.Unmarshal(data, &recordings); err != nil {
		t.Fatal(err)
	}
	if len(recordings) == 0 || recordings[0].Doorbot.Id == 0 {
		t.Fatal("recorded doorbot identity missing")
	}
}
