package ringapimodels

import (
	"math"

	"github.com/portpowered/go-ring/pkg/ringtypes"
)

// Status derives a concise health view without implying a battery on wired
// devices.
func (device DeviceDetailDevice) Status() DeviceStatus {
	status := DeviceStatus{
		BatteryPercent: nil,
		Connection:     nil,
		IsOffline:      false,
		PowerMode:      nil,
	}
	if device.Settings != nil {
		status.PowerMode = device.Settings.PowerMode
	}

	if device.Alerts != nil {
		status.Connection = device.Alerts.Connection
		status.IsOffline = device.Alerts.Connection != nil &&
			*device.Alerts.Connection == ringtypes.ConnectionState("offline")
	}

	if status.Connection == nil && device.Health != nil && device.Health.Connected != nil {
		state := ringtypes.ConnectionState("offline")
		if *device.Health.Connected {
			state = ringtypes.ConnectionState("online")
		}

		status.Connection = &state
		status.IsOffline = state == ringtypes.ConnectionState("offline")
	}

	if device.Health != nil && device.Health.BatteryPresent != nil && !*device.Health.BatteryPresent {
		return status
	}

	if status.PowerMode != nil && *status.PowerMode == ringtypes.PowerMode("wired") &&
		(device.Health == nil || device.Health.BatteryPresent == nil || !*device.Health.BatteryPresent) {
		return status
	}

	if device.Health != nil {
		switch {
		case device.Health.BatteryPercentage != nil:
			status.BatteryPercent = validBattery(*device.Health.BatteryPercentage)
		case device.Health.BatteryLevel != nil:
			status.BatteryPercent = validBattery(float64(*device.Health.BatteryLevel))
		}
	}

	if status.BatteryPercent == nil && (status.PowerMode == nil || *status.PowerMode != ringtypes.PowerMode("wired")) {
		if device.BatteryLife != nil {
			status.BatteryPercent = validBattery(float64(*device.BatteryLife))
		}
	}

	return status
}

func validBattery(value float64) *float64 {
	if math.IsNaN(value) || value < 0 || value > 100 {
		return nil
	}

	return &value
}
