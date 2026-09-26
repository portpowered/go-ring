package lint_test

import (
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// These assignments fail to compile if a closed public control regresses to
// an untyped string or an arbitrary settings map.
var (
	_ string                            = ring.SetVolumeRequest{}.DeviceID
	_ string                            = ring.UpdateDeviceHealthRequest{}.DeviceID
	_ bool                              = ring.SetLightsRequest{}.Enabled
	_ ringapimodels.SoundKind           = ring.TestSoundRequest{}.Sound
	_ string                            = ring.SetInHomeChimeRequest{}.DeviceID
	_ ringapimodels.InHomeChimeSettings = ring.SetInHomeChimeRequest{}.Settings
)
