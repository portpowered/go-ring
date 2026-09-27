package ringapimodels

// DeviceFamily represents the family/type of a Ring device
type DeviceFamily string

const (
	DeviceFamilyDoorbell   DeviceFamily = "doorbots"
	DeviceFamilyChime      DeviceFamily = "chimes"
	DeviceFamilyStickUpCam DeviceFamily = "stickup_cams"
	DeviceFamilyOther      DeviceFamily = "other"
)

// Device capabilities are reported only when an inventory field confirms them.
const (
	DeviceCapabilityLight             DeviceCapability = "light"
	DeviceCapabilityMotionDetection   DeviceCapability = "motion_detection"
	DeviceCapabilitySiren             DeviceCapability = "siren"
	DeviceCapabilityLiveView          DeviceCapability = "live_view"
	DeviceCapabilityPtzPanStep        DeviceCapability = "ptz_pan_step"
	DeviceCapabilityPtzTiltStep       DeviceCapability = "ptz_tilt_step"
	DeviceCapabilityPtzPanContinuous  DeviceCapability = "ptz_pan_continuous"
	DeviceCapabilityPtzTiltContinuous DeviceCapability = "ptz_tilt_continuous"
)

const (
	EventKindMotion   EventKind = "motion"
	EventKindDing     EventKind = "ding"
	EventKindOnDemand EventKind = "on_demand"
)
