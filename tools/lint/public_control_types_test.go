package lint_test

import (
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// These assignments fail to compile if a closed public control regresses to
// an untyped string or an arbitrary settings map.
var (
	_ ringapimodels.VolumeKind          = ring.SetVolumeRequest{}.Kind
	_ ringapimodels.LightState          = ring.SetLightsRequest{}.State
	_ ringapimodels.SoundKind           = ring.TestSoundRequest{}.Kind
	_ ringapimodels.InHomeChimeSettings = ring.SetInHomeChimeRequest{}.Settings
)
