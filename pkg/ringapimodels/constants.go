package ringapimodels

// DeviceFamily represents the family/type of a Ring device
type DeviceFamily string

const (
	DeviceFamilyDoorbell   DeviceFamily = "doorbots"
	DeviceFamilyChime      DeviceFamily = "chimes"
	DeviceFamilyStickUpCam DeviceFamily = "stickup_cams"
	DeviceFamilyOther      DeviceFamily = "other"
)

const (
	EventKindMotion   EventKind = "motion"
	EventKindDing     EventKind = "ding"
	EventKindOnDemand EventKind = "on_demand"
)

// SoundKind represents the type of sound for chime testing
type SoundKind string

const (
	SoundKindDing   SoundKind = "ding"
	SoundKindMotion SoundKind = "motion"
)

// Legacy device controls use these values in SetVolumeRequest and SetLightsRequest.
const (
	VolumeKindChime    = "chime"
	VolumeKindDoorbell = "doorbell"
	LightStateOn       = "on"
	LightStateOff      = "off"
)
