package ringapimodels

// Device represents a generic Ring device
type Device interface {
	GetID() string
	GetName() string
	GetFamily() DeviceFamily
	GetAddress() string
	GetTimezone() string
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

// GetAllDevices returns all devices as a slice of Device interfaces
func (d *DevicesResponse) GetAllDevices() []Device {
	var devices []Device
	for i := range d.Doorbells {
		devices = append(devices, &d.Doorbells[i])
	}
	for i := range d.Chimes {
		devices = append(devices, &d.Chimes[i])
	}
	for i := range d.StickUpCams {
		devices = append(devices, &d.StickUpCams[i])
	}
	for i := range d.Other {
		devices = append(devices, &d.Other[i])
	}
	return devices
}
