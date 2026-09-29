# Architecture and ownership

The public entry point is `pkg/ring`. It owns the client-facing API and the
association between explicit connection objects and their child sessions.
Transport I/O and media validation live in dependency packages.

```mermaid
flowchart LR
    App[Application] --> Client[ring.Client]
    Client --> HTTP[REST transport]
    HTTP --> OAuth[OAuth and session registration]
    HTTP --> API[Device, settings and recording APIs]
    Client -. opens .-> Connection[SignalingConnection]
    Client -. opens .-> AccountEvents[EventConnection]
    Client -. opens .-> Push[PushConnection]
    Connection --> Socket[dependencies/websocket]
    Socket --> Reader[One socket reader]
    Socket --> Events[Account event stream]
    Reader --> Sessions[DeviceSession registry]
    Socket --> Writer[Bounded priority writer]
    Sessions --> Writer
    Sessions --> RPC[RPC correlation and heartbeat policy]
    Sessions --> Media[dependencies/webrtc SDP and ICE validation]
    App --> Peer[Caller-owned WebRTC peer]
    Sessions -. SDP and ICE .-> Peer
```

`Client` owns reusable configuration and the REST transport. It retains no
account authorization or opened connections. Its compatibility `Close` method
does not close active connections. Call `Close` on each returned connection or
session to end its work. An `AuthContext` value is attached to each account-level
request and travels through the REST layer without changing shared client
authorization. Login sessions own their own PKCE state and cookies. A signaling,
event, or FCM push connection keeps the account selected when it opens; its
children cannot switch accounts. Each connection object owns its transport and
cancellation context. A `SignalingConnection` routes one WebSocket by dialog
identity. Its children own distinct device, signaling-session, and PTZ
control-session IDs.
The reader never runs user callbacks or blocks waiting for an application to
consume an event. Each child has a bounded event queue; overflow is observable
as a terminal backpressure error. Application contexts cancel their owned work.
The caller owns and closes the media peer separately.

An established playback or push dialog terminates on its own queue overflow;
other dialogs on that socket continue. Overflow during negotiation is fatal to
the connection because the negotiating child has not taken ownership and a
dropped protocol reply cannot be recovered. Connection failure publishes the
same terminal cause to every owned live, playback, and push child. A live
session's internal signaling state may finish first; its public `Wait` returns
only after wrapper cleanup has published the final result. Pan and tilt
continuous commands are serialized independently. A failed replacement command
retains the last acknowledged movement so `StopPTZ` and `Close` can still send
the corresponding zero-speed safety command.

| Code | Responsibility |
|---|---|
| `pkg/ring` | Public request/result types, client options, HTTP methods, session orchestration, and explicit connection ownership |
| `pkg/ringapimodels` | OpenAPI-generated public device, auth, event, and recording projections; handwritten behavior and typed errors |
| `pkg/generatedhttp`, `pkg/generatedsignaling` | Public generated protocol contract packages retained for existing import compatibility and included in release API comparison |
| `internal/generatedhttp`, `internal/generatedsignaling`, `internal/generatedfcm` | Canonical generated HTTP, signaling, and FCM wire models |
| `pkg/dependencies/rest` | Existing auth mechanisms and HTTP request/response adaptation; custom HTTP client support; safe-read retries |
| `pkg/dependencies/websocket` | Event-stream dial/read/close, signaling dial/read/write deadlines, bounded priority writer, and live-answer negotiation |
| `pkg/dependencies/webrtc` | SDP parsing, answer normalization, and ICE media-identity validation |
| `internal/protocol` | Verified service defaults, endpoint profiles, paths, signaling method and RPC constants |
| `internal/signaling` | Active-session RPC correlation, liveness/expiry and named policy defaults |
| `internal/testkit/replay` | Strict offline HTTP transport and scripted local WebSocket peers |
| `api` | Validated OpenAPI and AsyncAPI contracts; raw captured operations can exist without a public wrapper |
| `tests/replay/fixtures` | Actual sanitized exchanges, conversations, and schemas |
| `tests/replay` | Public API replay tests against local HTTP/WebSocket peers, with captured and labeled synthetic fixtures |
| `tests/integration` | Opt-in full end-to-end tests against real endpoints and hardware |
| `tools/protocols`, `tools/capture` | Schema validation and recording extraction tools |

New callers should depend on `pkg/ring`, not the transport packages. The
generated HTTP and signaling contract packages remain importable because they
were published before the private-wire boundary was adopted. Release API
comparison also covers the released dependency packages under `pkg/dependencies`
so existing consumers keep their Go imports. These are compatibility surfaces;
new callers should use `pkg/ring`. New handwritten transport state belongs in
`internal` or dependency packages.

The remaining transport scheduling in `pkg/ring` is the push/playback heartbeat
loops. Their public session methods can stay in `ring` while their ticker
mechanics move to `dependencies/websocket` in a further pass. Session-specific
RPC correlation and expiry already live in `internal/signaling`.

The Go client separates a persistent signaling connection from each device
session. Legacy inventory and account-event transports do not establish v3
discovery or signaling push behavior. Replay tests maintain the historical
comparison and test mapping.

Schemas are validated by official document tooling plus actual recorded
payloads. `api/openapi.yaml` defines HTTP operations using historical and
synthetic evidence;
`api/client-models.openapi.yaml` defines the public SDK projection. Both Go
model sets are generated by `make generate-api`. REST uses generated HTTP
models internally; the public client projects captured reads into client-owned
request and result types. SDK adapter types with behavior or custom number
decoding remain in `pkg/ring`; a contract test compares every JSON field and
required field with the projection schema. Device enumeration returns one
device list, with capabilities confirmed by inventory fields rather than
family classification.
Known legacy health fields are typed while unobserved hardware fields remain
extensible. Replay tests check the generated models against recorded bodies.

No reconnect silently repeats PTZ movement. Mutating HTTP calls are not
transport/server-error retried. An acknowledgement means a command was
accepted, and a lost acknowledgement leaves its outcome unknown. See
[signaling contract](../../api/asyncapi.yaml), [RTC session behavior](../guides/live-sessions.mdx),
and [client configuration](../guides/client-configuration.mdx) for the concrete rules.
