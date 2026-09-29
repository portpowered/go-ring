package replay_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/stretchr/testify/require"
)

func TestFCMNotificationShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, deviceID string
		action         ring.PushAction
	}{
		{"motion", "709739068", ring.PushActionMotion},
		{"intercom-unlocked", "1000", ring.PushAction("intercom_unlock")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join("fixtures", "push", test.name+".json"))
			require.NoError(t, err)

			event := ring.ParseFCMNotification(data)
			require.Equal(t, ring.PushMessage, event.Kind)
			require.Equal(t, test.deviceID, event.DeviceID)
			require.Equal(t, test.action, event.Action)
			require.JSONEq(t, string(data), string(event.Data))
		})
	}
}
