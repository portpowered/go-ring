// Package ringtypes contains schema-backed scalar types shared by the public
// API projections and generated provider models.
package ringtypes

import (
	"encoding/json"
	"strconv"
)

type scalarDecodeError struct {
	name  string
	cause error
}

func (failure scalarDecodeError) Error() string {
	return failure.name + ": " + failure.cause.Error()
}

func (failure scalarDecodeError) Unwrap() error { return failure.cause }

func wrapScalarDecodeError(name string, cause error) error {
	if cause == nil {
		return nil
	}

	return scalarDecodeError{name: name, cause: cause}
}

// BatteryReading accepts Ring's numeric and numeric-text percentage values.
type BatteryReading float64

func (reading *BatteryReading) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}

	var value json.Number
	{
		err := json.Unmarshal(data, &value)
		if err != nil {
			var text string

			err = json.Unmarshal(data, &text)
			if err != nil {
				return wrapScalarDecodeError("battery reading", err)
			}

			value = json.Number(text)
		}
	}

	percentage, err := strconv.ParseFloat(value.String(), 64)
	if err != nil {
		return wrapScalarDecodeError("battery reading", err)
	}

	*reading = BatteryReading(percentage)

	return nil
}

// OwnerID normalizes a numeric or textual provider identifier to a string.
type OwnerID string

func (id *OwnerID) UnmarshalJSON(data []byte) error {
	var text string
	if len(data) > 0 && data[0] == '"' {
		err := json.Unmarshal(data, &text)
		if err != nil {
			return wrapScalarDecodeError("owner identifier", err)
		}

		*id = OwnerID(text)

		return nil
	}

	var number int64

	err := json.Unmarshal(data, &number)
	if err != nil {
		return wrapScalarDecodeError("owner identifier", err)
	}

	*id = OwnerID(strconv.FormatInt(number, 10))

	return nil
}

// DeviceKind is an open provider hardware kind.
type DeviceKind string

// DeviceFamily is an open provider family label.
type DeviceFamily string

// ConnectionState is an open provider connectivity state.
type ConnectionState string

// PowerMode is an open provider power configuration.
type PowerMode string

// LocationResourceType is an open JSON:API resource type.
type LocationResourceType string

// TimelineEventType is an open provider event kind.
type TimelineEventType string

// RecordingStatus is an open provider recording status.
type RecordingStatus string

// TimelineState is an open provider timeline state.
type TimelineState string

// HistoryFeedType is an open provider history feed classification.
type HistoryFeedType string
