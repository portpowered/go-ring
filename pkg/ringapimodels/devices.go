package ringapimodels

// Device represents a generic Ring device
type Device interface {
	GetID() string
	GetName() string
	GetFamily() DeviceFamily
	GetAddress() string
	GetTimezone() string
}

// Doorbell represents a Ring doorbell device
type Doorbell struct {
	ID                     string        `json:"id"`
	Name                   string        `json:"name"`
	Family                 string        `json:"family"`
	Address                string        `json:"address"`
	Timezone               string        `json:"timezone"`
	WifiName               string        `json:"wifi_name"`
	WifiSignalStrength     int           `json:"wifi_signal_strength"`
	Volume                 int           `json:"volume"`
	HasLight               bool          `json:"has_light"`
	LightBrightness        *int          `json:"light_brightness,omitempty"`
	MotionDetectionEnabled bool          `json:"motion_detection_enabled"`
	Health                 *DeviceHealth `json:"health,omitempty"`
}

func (d *Doorbell) GetID() string {
	return d.ID
}

func (d *Doorbell) GetName() string {
	return d.Name
}

func (d *Doorbell) GetFamily() DeviceFamily {
	return DeviceFamilyDoorbell
}

func (d *Doorbell) GetAddress() string {
	return d.Address
}

func (d *Doorbell) GetTimezone() string {
	return d.Timezone
}

// Chime represents a Ring chime device
type Chime struct {
	ID                 string        `json:"id"`
	Name               string        `json:"name"`
	Family             string        `json:"family"`
	Address            string        `json:"address"`
	Timezone           string        `json:"timezone"`
	WifiName           string        `json:"wifi_name"`
	WifiSignalStrength int           `json:"wifi_signal_strength"`
	Volume             int           `json:"volume"`
	Health             *DeviceHealth `json:"health,omitempty"`
}

func (c *Chime) GetID() string {
	return c.ID
}

func (c *Chime) GetName() string {
	return c.Name
}

func (c *Chime) GetFamily() DeviceFamily {
	return DeviceFamilyChime
}

func (c *Chime) GetAddress() string {
	return c.Address
}

func (c *Chime) GetTimezone() string {
	return c.Timezone
}

// StickUpCam represents a Ring StickUp camera device
type StickUpCam struct {
	ID                     string        `json:"id"`
	Name                   string        `json:"name"`
	Description            string        `json:"description,omitempty"`
	Family                 string        `json:"family"`
	Address                string        `json:"address"`
	Timezone               string        `json:"timezone"`
	WifiName               string        `json:"wifi_name"`
	WifiSignalStrength     int           `json:"wifi_signal_strength"`
	Volume                 int           `json:"volume"`
	HasLight               bool          `json:"has_light"`
	LightBrightness        *int          `json:"light_brightness,omitempty"`
	MotionDetectionEnabled bool          `json:"motion_detection_enabled"`
	Health                 *DeviceHealth `json:"health,omitempty"`
}

func (s *StickUpCam) GetID() string {
	return s.ID
}

func (s *StickUpCam) GetName() string {
	return s.Name
}

func (s *StickUpCam) GetFamily() DeviceFamily {
	return DeviceFamilyStickUpCam
}

func (s *StickUpCam) GetAddress() string {
	return s.Address
}

func (s *StickUpCam) GetTimezone() string {
	return s.Timezone
}

// Other represents a Ring "other" device (e.g., Intercom)
type Other struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Family   string        `json:"family"`
	Address  string        `json:"address"`
	Timezone string        `json:"timezone"`
	Kind     string        `json:"kind"`
	Health   *DeviceHealth `json:"health,omitempty"`
}

func (o *Other) GetID() string {
	return o.ID
}

func (o *Other) GetName() string {
	return o.Name
}

func (o *Other) GetFamily() DeviceFamily {
	return DeviceFamilyOther
}

func (o *Other) GetAddress() string {
	return o.Address
}

func (o *Other) GetTimezone() string {
	return o.Timezone
}

// DeviceHealth represents health information for a device
type DeviceHealth struct {
	BatteryLevel    *int    `json:"battery_level,omitempty"`
	BatteryStatus   *string `json:"battery_status,omitempty"`
	SignalStrength  *int    `json:"signal_strength,omitempty"`
	FirmwareVersion *string `json:"firmware_version,omitempty"`
	LastUpdate      *string `json:"last_update,omitempty"`
}

// DevicesResponse represents the response from listing devices
type DevicesResponse struct {
	Doorbells   []*Doorbell   `json:"doorbots"`
	Chimes      []*Chime      `json:"chimes"`
	StickUpCams []*StickUpCam `json:"stickup_cams"`
	Other       []*Other      `json:"other"`
}

// GetAllDevices returns all devices as a slice of Device interfaces
func (d *DevicesResponse) GetAllDevices() []Device {
	var devices []Device
	for _, db := range d.Doorbells {
		devices = append(devices, db)
	}
	for _, ch := range d.Chimes {
		devices = append(devices, ch)
	}
	for _, sc := range d.StickUpCams {
		devices = append(devices, sc)
	}
	for _, other := range d.Other {
		devices = append(devices, other)
	}
	return devices
}
