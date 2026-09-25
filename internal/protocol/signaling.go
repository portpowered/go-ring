package protocol

// Signaling wire methods. These names follow the committed AsyncAPI payloads;
// lifecycle defaults belong to internal/signaling/policy.go instead.
const (
	MethodLiveView        = "live_view"
	MethodPlayback        = "playback"
	MethodSDP             = "sdp"
	MethodICE             = "ice"
	MethodSessionCreated  = "session_created"
	MethodActivateSession = "activate_session"
	MethodCameraStarted   = "camera_started"
	MethodCameraOptions   = "camera_options"
	MethodMicEnable       = "mic_enable"
	MethodStreamOptions   = "stream_options"
	MethodClose           = "close"
	MethodPing            = "ping"
	MethodPong            = "pong"
	MethodRPC             = "rpc"
	RPCPanStep            = "PTZ.Pan.Step"
	RPCTiltStep           = "PTZ.Tilt.Step"
	RPCPanContinuous      = "PTZ.Pan.Continuous"
	RPCTiltContinuous     = "PTZ.Tilt.Continuous"
	RPCPanHalted          = "PTZ.Pan.Halted"
	JSONRPCVersion        = "2.0"
	PTZVersion            = 1
	PanLeft               = "LEFT"
	PanRight              = "RIGHT"
	TiltUp                = "UP"
	TiltDown              = "DOWN"
)
