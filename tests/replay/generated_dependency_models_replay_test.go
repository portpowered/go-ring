package replay_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/pkg/dependencymodels"
)

func TestGeneratedDependencyDeviceHealthFromRecording(t *testing.T) {
	x := deviceListExchange(t, "https://api.ring.com")
	var body struct {
		Devices []dependencymodels.RingDevice `json:"devices"`
	}
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
	if _, ok := health.Get("ptz_connected"); !ok {
		t.Fatal("unobserved hardware health field lost")
	}
}

func TestGeneratedDependencyRecordingFromLegacyFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("fixtures", "legacy", "ring_doorbot_history.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recordings []dependencymodels.RingRecording
	if err := json.Unmarshal(data, &recordings); err != nil {
		t.Fatal(err)
	}
	if len(recordings) == 0 || recordings[0].Doorbot.ID == 0 {
		t.Fatal("recorded doorbot identity missing")
	}
	if recordings[0].DeviceID != 0 {
		t.Fatal("derived device ID unexpectedly decoded from wire")
	}
}
