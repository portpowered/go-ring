# dgreif feature gap plan

## v0.4.0 implementation checkpoint

The captured device detail and list responses now have named OpenAPI fields throughout the observed device, health, features, and settings trees. The generated model replay checks for captured fields that would otherwise fall into `AdditionalProperties`. Legacy family fixtures exposed mixed wire encodings for `battery_life`, `enable_vod`, and `motion_zones`; the schema now accepts those variants. Fields observed only as null remain marked as such rather than assigned an invented meaning.

`Client.GetDeviceStatus` and `DeviceDetailDevice.Status` expose connection and battery state without treating a wired camera's `battery_life: 100` as proof of a battery. Numeric text is accepted; an out-of-range hardware value such as 4081 remains unknown. `Client.UnlockIntercom` uses a generated JSON-RPC request body and has source-contract replay, but this camera account cannot verify unlock or a physical door state.

`Client.ConnectPush` owns a separate, optional FCM receiver. Auth and persisted FCM credentials are supplied by the caller, and `Close` ends the connection. Ring token registration and per-device ding/motion subscriptions have source-contract replays. A live watch on the camera received FCM credentials, Ring accepted the registration, and the receiver connected. No motion notification arrived during the watch window, so ding, motion, and unlocked event delivery remain unverified live. The first Go receiver attempt failed with HTTP 401 at FCM registration because it included the default VAPID key; the adapter omits that key and the live registration succeeds. The CLI can repeat the check with `events watch <camera-id>`.

The remaining device gap is principally event payload confirmation on a triggered camera/intercom, intercom unlock response verification, and the P2 chime, location, and hub functions listed below. Keep those source-only cases distinct from captured and live-tested behavior in release notes.

This plan compares the public Go client with the current dgreif Ring client. It is an inventory of behavior to validate, not a promise that a similarly named Ring endpoint still works. Use captured traffic and live verification ahead of another library's implementation when they disagree. The customer-facing API remains account-scoped through `AuthContext` and device IDs; it should not require callers to retain a mutable camera or intercom object.

Sources: [dgreif API](https://github.com/dgreif/ring/blob/main/packages/ring-client-api/api.ts), [intercom](https://github.com/dgreif/ring/blob/main/packages/ring-client-api/ring-intercom.ts), [camera](https://github.com/dgreif/ring/blob/main/packages/ring-client-api/ring-camera.ts), [chime](https://github.com/dgreif/ring/blob/main/packages/ring-client-api/ring-chime.ts), and [location/hub](https://github.com/dgreif/ring/blob/main/packages/ring-client-api/location.ts). Go inventory: `pkg/ring/interface.go`, `api/openapi.yaml`, `api/asyncapi.yaml`, `docs/plans/parity-matrix.md`, and the sanitized replay fixtures.

## Corrected starting matrix

| Capability | Go today | Evidence and next step |
| --- | --- | --- |
| Camera siren | `Client.SetSiren` exists, including on/off replay and CLI use | Captured on and off. Document it in the public operation table; no new endpoint required. |
| Camera light | `Client.SetLights` exists | Legacy synthetic replay, no matching C1 request. Keep working route; verify live on/off on a capable camera. |
| Camera/intercom battery and offline status | `GetDeviceDetail().Device.Health` exposes `battery_level` and `connected` when present; `ListDevices()` exposes a narrower `Health`. Neither currently exposes the captured top-level `battery_life`, `alerts.connection`, or `external_connection`. | The C1 device detail contains `battery_life: 100` and `alerts.connection: online` outside `health`. These fields are in the wire JSON but are dropped by the typed/public projections. Extend the existing device schema and public models before adding any convenience status method. Missing does not mean offline or zero battery. |
| Intercom unlock | No Go method | dgreif sends PUT `/commands/v1/devices/{id}/device_rpc` with JSON-RPC `unlock_door`, `door_id: 0`, `user_id: 0`. The Go schema has a generic command route, but no unlock contract or matching capture. Add only after a new trace or opt-in live validation establishes response and error semantics. |
| Ding, motion, unlocked notifications | Go has generic `ConnectEvents` and signaling `SubscribePush` | The captured signaling push replay proves `shoulder_tap` only. It does not prove delivery of ding, motion, or intercom unlock. dgreif uses an FCM receiver plus per-device subscribe calls for those events. |
| Dedicated camera/intercom objects | No equivalent | Do not make these mandatory: IDs plus `AuthContext` preserve multiplexing across accounts. Add typed device family/capability views and optional pure status helpers, not stateful auth-bearing device handles. |
| History, recording, WebRTC, PTZ | Go already implements these | Keep the existing, replay-tested surfaces. |

## Proposed public API

Keep `Client` stateless with respect to authorization. Each new request includes `Auth AuthContext`; every operation takes a device or location ID, not an entire device object. Proposed additions:

```go
type UnlockIntercomRequest struct {
    Auth AuthContext
    DeviceID string
    // Omitted door/user selectors use recorded defaults only if verified.
}
func (*Client) UnlockIntercom(context.Context, UnlockIntercomRequest) error

type DeviceStatus struct {
    BatteryPercent *int
    Connectivity Connectivity // online, offline, unknown
    ObservedAt time.Time
    Source StatusSource // device detail, listing, push, or polling
}
func (*Client) GetDeviceStatus(context.Context, GetDeviceStatusRequest) (DeviceStatus, error)
```

The final unlock request should expose `DoorID` or `UserID` only if evidence shows callers need them. An accepted RPC is not proof that the door opened; report acknowledgement separately from an eventual unlock notification. Battery percentage needs a documented rule for two batteries and string-valued `battery_life`; an unknown value remains nil. Offline should be a three-state value, because absent/stale health cannot safely mean online. `health.connected` and `alerts.connection` are separate signals and should remain distinguishable in the raw typed model.

For events, use one account-scoped, explicitly owned `EventStream`, opened with `AuthContext`, filters (device IDs and typed event kinds), and an optional credential store. `Receive(ctx)` returns typed `DingEvent`, `MotionEvent`, `IntercomUnlockedEvent`, or an `UnknownEvent` retaining raw payload; `Close()` ends registration and workers. Do not hide a long-lived subscription inside `Client` or merge it with `DeviceSession`, which is a live/playback signaling lifetime. Keep the existing signaling subscription and account WebSocket transport as distinct providers behind the same normalized event model only after actual event payloads are mapped. Document whether a provider supports each event kind.

FCM is a transport candidate, not yet the default. dgreif establishes an FCM receiver, PATCHes a device registration containing its push token, POSTs doorbot ding/motion subscriptions, renews registration when credentials or sessions change, and dispatches device notifications. Implement this in a separate `dependencies/push` package only if a focused trace or live experiment confirms the chain still works for our accounts. Its credential persistence must be caller supplied and scoped per account; token rotation, restart, reconnect, deduplication, backpressure, and unregister/close need explicit behavior. A server multiplexing customers would own one active event stream per account that needs notifications; ordinary HTTP calls would remain stateless. Polling can refresh device state, but should not be advertised as real-time ding/unlock delivery. The captured signaling `shoulder_tap` stream is worth testing as an alternative before adopting FCM.

## Additional dgreif methods to triage

| Priority | dgreif behavior | Go status / decision |
| --- | --- | --- |
| P1 | Intercom `unlock`, ding/unlocked event subjects, subscribe/unsubscribe | Missing typed surface; acquire unlock and notification traces first. |
| P1 | Camera `getHealth`, battery/offline/charging/low-battery properties, push ding/motion | Raw health exists in Go; normalize status and prove event transport. |
| P2 | Camera event search, `videoSearch`, periodic footage, UUID/next snapshot | Go has history, recordings, and legacy snapshot but not all these distinct routes. Add typed methods only for useful recorded/live-verified variants; document snapshot freshness and battery cost. |
| P2 | Chime ringtone listing/selection, snooze/clear, nightlight, linked doorbells | Go has test sound and volume. Capture and schema each endpoint separately; do not infer it from `TestSound`. |
| P2 | Profile and Amazon Key lock associations | Missing. Profile may be a small read-only addition; treat lock integrations as a separate product scope. |
| P3 | Hub device stream, alarm modes/siren, security-panel state, rooms, location mode/settings/sharing, panic dispatch | dgreif's `Location` and `RingDevice` form a second stateful protocol. Inventory and capture separately; do not conflate hub siren with camera `SetSiren`. Panic dispatch requires a dedicated product decision and live verification. |

## Work sequence and acceptance tests

1. Extend the parity matrix with one row per candidate method: dgreif source, Go method, exact path/message, capture ID, replay fixture, live result, and decision. Extract any matching operations from the existing mitmproxy recording without treating a text match as a validated conversation. Seek targeted new captures for intercom unlock, ding/motion/unlock notifications, FCM registration, and subscription renewal.
2. First add the already captured top-level `battery_life`, optional `battery_life_2`, `alerts.connection`, `external_connection`, and relevant `settings.power_mode` to the existing OpenAPI device schema, then regenerate the wire model and expose them in the public `DeviceDetail`/inventory projection. Retain the existing `health.battery_level` and `health.connected`. Add table-driven replay for battery absent/number/string/two-battery, disconnected/offline/unknown, and stale observations. Only then consider a convenience `GetDeviceStatus` projection; it needs no new Ring endpoint.
3. Add intercom unlock after its wire contract is established. Specify request/response/error schemas in OpenAPI, generate the REST models/client, and add focused replay for accepted, rejected, malformed, timeout, and wrong-device cases. Verify acknowledgement versus actual unlock as separate outcomes.
4. Run an event transport proof with a real intercom and camera: trigger ding, motion, and unlock; capture server subscription/registration and payloads; compare signaling push, account events, and FCM. Choose provider per capability based on observed delivery, not source-library preference.
5. Implement typed `EventStream` and any required FCM adapter. Put push registration and transport lifecycle in `dependencies/push`, normalization in `pkg/ring`, and protocol schemas in OpenAPI/AsyncAPI as appropriate. Replay tests must cover each event independently plus reconnect, token rotation, duplicate delivery, slow consumer, cancel, close, and account isolation under `-race`.
6. Implement the P2 operations in small endpoint groups, each with schema, replay fixtures, typed public method, README example, and documentation of unsupported device families. Treat hub/location operations as a separate milestone after their protocol is captured.

Completion for each operation requires a typed public API, a documented support/limitation entry, a focused sanitized replay case, appropriate failure cases, and either live verification or an explicit `source-only/unverified` designation. Do not use line coverage alone as the gate for event correctness.
