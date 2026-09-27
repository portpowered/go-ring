# RTC streaming procotol

The ring APIs uses a websocket connection as a signalling channel.
The signalling channel is used to establish the RTC session, as well as a keep alive connection.
When the signalling channel is closed, the RTC session is disconnected.


The connection logic can be viewed as roughly:
1. The customer calls the websocket ticket API to receive a ticket
2. The customer uses that ticket to establish a websocket connection
3. The customer uses that websocket connection to signal an SDP offer
4. The customer receives an SDP answer over the websocket connection
5. The customer persists the websocket connection and ping/pongs at a fixed cadence to maintain connection liveness
6. Both the websocket and the RTC connection are maintained and persisted for a fixed duration.



## Connections and device sessions

`Client.OpenSignaling` opens an authenticated WebSocket. Its
`SignalingConnection.StartDeviceSession` creates a child device session from a
caller-provided SDP offer. `DeviceSession` owns negotiated identity, heartbeat,
SDP/ICE exchange, microphone/stream controls, and PTZ requests. This connection
is broader than an RTC media stream; the caller's WebRTC stack transports media.

Session methods include `Answer`, `SendICE`, `PanStep`, `TiltStep`,
`PanContinuous`, `TiltContinuous`, `StopPTZ`, `SetMicrophone`, `SetStreamOptions`,
`Receive`, `Wait`, and `Close`. PTZ results acknowledge commands; they do not
prove physical positioning. Device capabilities vary. Zoom is not verified.
Continuous pan and tilt speeds are normalized numbers from `0` to `1`,
inclusive. `0` requests a stop; `StopPTZ` also uses a zero-speed command for a
tracked movement. Values above `1`, negative values, NaN, and infinities are
rejected before sending. Step commands take only a direction.

Close the session when finished, close the connection to release its children,
and close the client when its work is done. Keep the connection and session
contexts alive for their intended lifetime. Cancellation ends owned work.
Sessions have a maximum lifetime of 60 minutes, an SDK policy rather than a
proven vendor timeout. See [session design](../plans/session-design.md) for SDP
construction, identity domains, heartbeat and teardown rules; sections marked
as target behavior remain implementation requirements.

| Surface | Evidence and limits |
|---|---|
| Authentication and existing HTTP methods | Go regression tests; the new capture contains no OAuth token exchange. |
| v3 device inventory | Recorded Go replay tests; legacy inventory fixtures do not establish the v3 route. |
| SDP and PTZ | Captured conversation/schema replay plus local connection tests. Signaling ticket bootstrap is separately based on the existing POST mechanism. |
| Other captured HTTP routes | Committed schemas and exchanges; presence in OpenAPI does not imply a public SDK method. |
| Push and playback | Present in recordings; full public abstractions remain planned. Existing event WebSocket behavior is experimental. |

The captured GET `/api/v1/clap/tickets` has not been proven equivalent to the
existing POST signaling ticket bootstrap. Offline replay is not a live
compatibility guarantee for every model, region, or account.
