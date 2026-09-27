package main

const (
	deviceIDKey           = "device_id"
	deviceIDPlaceholder   = "{device_id}"
	requestIDKey          = "requestid"
	locationIdentifier    = "location"
	clientToServer        = "client_to_server"
	serverToClient        = "server_to_client"
	deviceListPath        = "/device_info/v3/devices"
	utf16BigEndian        = "utf-16be"
	numericIdentifierBase = int64(1000)
	unixSecondsBase       = int64(1_700_000_000)
	unixMillisecondsBase  = int64(1_700_000_000_000)
	millisecondsThreshold = int64(100_000_000_000)
	sdpOriginFieldCount   = 6
	byteShiftBits         = 8
	fixtureDirectoryMode  = 0o750
)
