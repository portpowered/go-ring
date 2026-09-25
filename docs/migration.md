# Migration during the library improvement work

These changes describe the current development checkout. Select a release or
commit containing them; no released-version support is implied.

| Existing behavior | Current behavior / migration |
|---|---|
| `StartRTCStream` returns a legacy RTC handle | Removed. Use `OpenSignaling` → `StartDeviceSession`; the caller supplies SDP and owns its WebRTC media stack. |
| `StopRTCStream` ignored its ID | Removed. Close the `DeviceSession` handle, then the shared `SignalingConnection` when finished. |
| `Client.Close` only set a flag | Closes owned signaling connections and live device sessions. |
| Handwritten `ClientInterface` | Removed. HTTP operations and models are generated in `pkg/generatedhttp` by `oapi-codegen`; signaling models are generated in `pkg/generatedsignaling` by Modelina. Define a small interface at the call site for domain-level mocking. |
| `WithRTCWebSocketURL` | Renamed to `WithSignalingWebSocketURL`; it configures the shared signaling transport used by live view, playback, and push. |
| HTTP transport/server errors retried mutations | Automatic retries are restricted to GET/HEAD. A mutation can have succeeded even if its reply was lost; callers decide whether retrying is appropriate. |
| Error helper classification required the outermost concrete type | `ringapimodels.Is...Error` and `IsHTTPStatusCode` inspect wrapped errors. `errors.Is` / `errors.As` remain available. |
| HTTP error formatting included raw response bodies | `HTTPError.Error()` omits raw bodies; `Body` remains available for deliberate inspection. |
| Endpoint overrides were scattered | `WithRegion` and `WithEndpoints` configure one client. Explicit endpoints win independent of option order. EU/FE Solutions bootstrap needs a supplied, verified endpoint. |
| Generic control requests used `/clients_api/ring_devices/{id}` with unverified JSON bodies | `SetVolume` now needs `Kind` and `Description` and sends family-specific query values; `SetInHomeChime` needs `Description` and one setting; `SetLights` uses legacy on/off paths and rejects `Duration`; `TestSound` uses the chime sound path and query; `SetMotionDetection` uses the captured settings PATCH. Update call sites that depended on the old shapes. |

Authentication mechanisms and token persistence ownership remain unchanged.
The new capture does not contain an OAuth exchange. Typed settings currently
expose the recorded motion-detection field; they do not claim all captured
settings are supported consumer APIs.

The new session API uses independent device, dialog, signaling-session, and
PTZ control-session identities. Do not copy a signaling ID into JSON-RPC
`params.sessionId`. The SDK creates and correlates command IDs. PTZ success is
an acknowledgement, not confirmation of a final camera position. The 60-minute
maximum is SDK policy, with earlier cancellation available to the caller.

`SignalingConnection.StartPlayback` negotiates a cloud playback SDP session;
`SubscribePush` registers filters, sends subscription heartbeats, and receives
typed push events. Close each handle when finished. The older `/clients_api/ws`
event connection remains experimental and is a different protocol. Captured
replay tests cover these new paths; live media decoding and reconnect/resume
behavior remain unverified. See [session design](session-design.md).
