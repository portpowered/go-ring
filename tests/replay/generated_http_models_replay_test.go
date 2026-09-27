package replay_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/pkg/generatedhttp"
)

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
