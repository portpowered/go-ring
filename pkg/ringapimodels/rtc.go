package ringapimodels

// RTCStream represents a WebRTC stream for live video
type RTCStream struct {
	StreamID      string
	DeviceID      int64
	SDPOffer      string
	SDPAnswer     string
	ICECandidates []string
	StreamURL     string
}

// RTCStreamConfig represents configuration for starting an RTC stream
type RTCStreamConfig struct {
	DeviceID int64
}
