# Migration during the library improvement work

These changes describe the current development checkout. Select a release or
commit containing them; no released-version support is implied.

| Existing behavior | Current behavior / migration |
|---|---|
| `StartRTCStream` returns a legacy RTC handle | Still available. New persistent controls use `OpenSignaling` → `StartDeviceSession`. The caller supplies SDP and owns its WebRTC media stack. |
| `StopRTCStream` ignored its ID | Now closes a registered stream selected by `GetStreamID`; unknown IDs return a typed not-found error. Use the handle's idempotent `Close` when you already own it. |
| `Client.Close` only set a flag | Closes owned new signaling connections and registered legacy RTC streams. |
| HTTP transport/server errors retried mutations | Automatic retries are restricted to GET/HEAD. A mutation can have succeeded even if its reply was lost; callers decide whether retrying is appropriate. |
| Error helper classification required the outermost concrete type | `ringapimodels.Is...Error` and `IsHTTPStatusCode` inspect wrapped errors. `errors.Is` / `errors.As` remain available. |
| HTTP error formatting included raw response bodies | `HTTPError.Error()` omits raw bodies; `Body` remains available for deliberate inspection. |
| Endpoint overrides were scattered | `WithRegion` and `WithEndpoints` configure one client. Explicit endpoints win independent of option order. EU/FE Solutions bootstrap needs a supplied, verified endpoint. |

Authentication mechanisms and token persistence ownership remain unchanged.
The new capture does not contain an OAuth exchange. Typed settings currently
expose the recorded motion-detection field; they do not claim all captured
settings are supported consumer APIs.

The new session API uses independent device, dialog, signaling-session, and
PTZ control-session identities. Do not copy a signaling ID into JSON-RPC
`params.sessionId`. The SDK creates and correlates command IDs. PTZ success is
an acknowledgement, not confirmation of a final camera position. The 60-minute
maximum is SDK policy, with earlier cancellation available to the caller.

Push and playback remain separate planned abstractions. The existing event
WebSocket implementation remains experimental. See [porting progress](porting-progress.md)
for tests and explicit gaps; see [session design](session-design.md) for target
contracts still being completed.
