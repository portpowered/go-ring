package protocols_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/portpowered/go-ring/pkg/ring"
)

// SDK projections keep behavioral methods and custom number decoding in ring.
// Their complete JSON field inventory and required fields still come from the
// checked-in public projection schema.
func TestHandwrittenClientProjectionFieldsMatchSchema(t *testing.T) {
	t.Parallel()

	contract := loadOpenAPI(t, "client-models.openapi.yaml")
	projections := map[string]reflect.Type{
		"DeviceDetail":          reflect.TypeFor[ring.DeviceDetail](),
		"DeviceDetailDevice":    reflect.TypeFor[ring.DeviceDetailDevice](),
		"DeviceAlerts":          reflect.TypeFor[ring.DeviceAlerts](),
		"DeviceStatus":          reflect.TypeFor[ring.DeviceStatus](),
		"DeviceOwner":           reflect.TypeFor[ring.DeviceOwner](),
		"DeviceLegacySettings":  reflect.TypeFor[ring.DeviceLegacySettings](),
		"DeviceDetailHealth":    reflect.TypeFor[ring.DeviceDetailHealth](),
		"DeviceFeatures":        reflect.TypeFor[ring.DeviceFeatures](),
		"VideoRenderingFeature": reflect.TypeFor[ring.VideoRenderingFeature](),
		"FeatureAvailability":   reflect.TypeFor[ring.FeatureAvailability](),
		"FeatureEligibility":    reflect.TypeFor[ring.FeatureEligibility](),
		"FeatureEnablement":     reflect.TypeFor[ring.FeatureEnablement](),
		"LocationAddress":       reflect.TypeFor[ring.LocationAddress](),
		"LocationSummary":       reflect.TypeFor[ring.LocationSummary](),
		"GeoCoordinates":        reflect.TypeFor[ring.GeoCoordinates](),
		"LocationList":          reflect.TypeFor[ring.LocationList](),
		"LocationAttributes":    reflect.TypeFor[ring.LocationAttributes](),
		"LocationResource":      reflect.TypeFor[ring.LocationResource](),
		"LocationDetail":        reflect.TypeFor[ring.LocationDetail](),
		"LocationMeta":          reflect.TypeFor[ring.LocationMeta](),
		"LocationGroup":         reflect.TypeFor[ring.LocationGroup](),
		"LocationGroups":        reflect.TypeFor[ring.LocationGroups](),
		"LocationGroupDevices":  reflect.TypeFor[ring.LocationGroupDevices](),
		"TimelineDevice":        reflect.TypeFor[ring.TimelineDevice](),
		"TimelineEvent":         reflect.TypeFor[ring.TimelineEvent](),
		"DeviceTimeline":        reflect.TypeFor[ring.DeviceTimeline](),
		"HistoryFeedItem":       reflect.TypeFor[ring.HistoryFeedItem](),
		"HistoryDevices":        reflect.TypeFor[ring.HistoryDevices](),
		"CapturedTickets":       reflect.TypeFor[ring.CapturedTickets](),
	}

	for name, model := range projections {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			projection := contract.doc.Components.Schemas[name]
			if projection == nil || projection.Value == nil {
				t.Fatalf("%s has no public projection schema", name)
			}

			actual, required := projectionJSONFields(model)
			expected := make([]string, 0, len(projection.Value.Properties))

			for field := range projection.Value.Properties {
				expected = append(expected, field)
			}

			slices.Sort(expected)
			slices.Sort(projection.Value.Required)

			if !slices.Equal(actual, expected) {
				t.Errorf("%s JSON fields = %v; schema = %v", name, actual, expected)
			}

			if !slices.Equal(required, projection.Value.Required) {
				t.Errorf("%s required JSON fields = %v; schema = %v", name, required, projection.Value.Required)
			}
		})
	}
}

func projectionJSONFields(model reflect.Type) ([]string, []string) {
	fields := make([]string, 0, model.NumField())
	required := make([]string, 0)

	for index := range model.NumField() {
		field := model.Field(index)

		name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}

		fields = append(fields, name)

		if options != "omitempty" {
			required = append(required, name)
		}
	}

	slices.Sort(fields)
	slices.Sort(required)

	return fields, required
}
