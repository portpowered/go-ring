package signaling

import "time"

// SDK policy defaults. Negotiated heartbeat intervals are session state, while
// these limits are local resource and lifecycle rules, not vendor guarantees.
const (
	MaxSessionAge            = 60 * time.Minute
	MaxHeartbeatInterval     = time.Minute
	DefaultHeartbeatInterval = 5 * time.Second
	MissedHeartbeatIntervals = 3
	RPCResponseTimeout       = 10 * time.Second
	HandshakeTimeout         = 10 * time.Second
	NegotiationTimeout       = 30 * time.Second
	SendTimeout              = 10 * time.Second
	CloseTimeout             = 2 * time.Second
	EventQueueCapacity       = 32
	NegotiationQueueCapacity = 128
	MaxMessageBytes          = 1 << 20
	MaxTicketResponseBytes   = 1 << 20
)
