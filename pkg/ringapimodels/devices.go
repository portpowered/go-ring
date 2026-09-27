package ringapimodels

// Supports reports whether inventory explicitly confirms a device operation.
// False also covers unknown support, so callers must not treat it as proof
// that the server rejects an operation.
func (d Device) Supports(capability DeviceCapability) bool {
	for _, supported := range d.Capabilities {
		if supported == capability {
			return true
		}
	}
	return false
}
