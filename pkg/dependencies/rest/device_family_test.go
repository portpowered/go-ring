package rest

import (
	"testing"

	"github.com/portpowered/go-ring/pkg/dependencymodels"
)

func TestPythonDeviceKindFamilies(t *testing.T) {
	for _, tc := range []struct{ kind, want string }{
		{"doorbell_oyster", "doorbell"}, {"lpd_v4", "doorbell"}, {"df_doorbell_clownfish", "doorbell"},
		{"chime_pro_v2", "chime"}, {"hp_cam_v1", "camera"}, {"hp_cam_v2", "camera"},
		{"stickup_cam_mini_ptz_v1", "camera"}, {"cocoa_camera", "camera"},
		{"intercom_handset_video", "other"}, {"beams_ct200_transformer", "other"},
		{"future_model", "other"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			if got := classifyDevice(dependencymodels.RingDevice{Kind: tc.kind}); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	if got := classifyDevice(dependencymodels.RingDevice{Kind: "future_model", Family: "stickup_cams"}); got != "camera" {
		t.Fatalf("family fallback = %s", got)
	}
	for _, tc := range []struct{ family, want string }{{"doorbots", "doorbell"}, {"chimes", "chime"}} {
		if got := classifyDevice(dependencymodels.RingDevice{Kind: "future_model", Family: tc.family}); got != tc.want {
			t.Fatalf("family %s mapped to %s", tc.family, got)
		}
	}
}
