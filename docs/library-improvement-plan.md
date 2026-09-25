# go-ring: third-party library design and testing plan

Status: implementation in progress. API examples below describe the target design unless explicitly marked current. See [porting progress](porting-progress.md) for the Python baseline, recording comparisons, verified implementation, and outstanding gaps. Follow the execution order in [the porting process](internal/process-of-reverse-engineering.md): reference architecture and tests, shared replay contracts, mapped Go tests, implementation, then verification.

Design supplements: [feature/wire/session parity matrix](parity-matrix.md) and [session API, SDP construction and lifecycle contract](session-design.md). These define the revised public shape: Client -> SignalingConnection -> DeviceSession, with separate EventSubscription and PlaybackSession scopes. Intentional breaking changes are accepted to make the feature set clear; RTCStream is an existing type to migrate away from.

Concrete implementation contracts: [recording/cassette/scenario formats](replay-format.md) and [constants and configuration ownership](constants-and-configuration.md).

## 1. Outcome and boundaries

Make go-ring a dependable, medium-level Go SDK: typed requests and responses, context-aware operations, injectable network dependencies, explicit session ownership, documented errors, and reproducible offline tests. Follow `docs/internal/makings-of-a-good-library.md` and `docs/internal/process-of-reverse-engineering.md`.

Preserve supported behavior and existing auth mechanisms while allowing deliberate API renames and breaking changes. Separate protocol details from the public model. Reach at least 90% statement coverage of maintained library code, with explicit behavioral coverage for failures and concurrent session lifecycles. Passing replay tests demonstrates behavior against recorded inputs, not universal Ring device support.

The Python project is a behavioral reference, not an instruction to duplicate its class hierarchy, sync/async wrappers, routes or every feature. Prefer verified working Go behavior and direct recording evidence over Python where they diverge. Full Python parity is not a release requirement; track feature gaps and intentional differences explicitly. A mock-only passing test is not vendor verification. Scope conflicting evidence by device, endpoint version and environment rather than assuming one universal shape. Authentication is already supported: preserve its API and mechanisms and add regression coverage without redesigning it. Prioritize device discovery/health, existing controls, recordings, and a persistent signaling session with PTZ. Treat groups, intercom, and additional mutations as separate feature decisions; captured push subscriptions are a distinct capability on the signaling connection.

## 2. Baseline and immediate findings

| Area | Inspected state | Planned action |
|---|---|---|
| Public API | `pkg/ring/interface.go`, models in `pkg/ringapimodels` | Keep an accessible API index; introduce clear connection/session names with a breaking migration |
| Transport | `pkg/dependencies/rest`, wire models in `pkg/dependencymodels` | Move implementation details under `internal` with compatibility decisions first |
| Tests | Auth, controls, devices, recordings, events, RTC; external test packages and local servers | Extend existing tests and make their assertions stricter |
| Coverage | Verified unit run reported 69.8% with explicit library instrumentation | Establish a repeatable library-only denominator and enforce 90% in CI |
| RTC lifecycle | `StopRTCStream` ignores its ID and returns nil; `Client.Close` only marks the client closed | Add regression tests and define ownership before refactoring |
| Events | Default WebSocket endpoint is explicitly experimental in source | Do not claim Python push parity or capture verification |
| HTTP mock | Matches method/path; returns stored response objects | Match host/query/body and return fresh bodies per request |
| RTC tests | Several tests synchronize using sleeps | Use scripted peer acknowledgements, fake clock, and bounded waits |
| README | Useful badges and examples; auth snippet has undefined variables | Replace with compiled examples and evidence-qualified support matrix |
| CI | OS/Go matrix, race tests, vet, formatting, module checks, Codecov upload | Keep these; add local coverage gate, contracts, and replay checks |

The full default test run passed. The verified coverage baseline used PowerShell: `$env:GOWORK='off'; go test ./test/unit/... '-coverpkg=./pkg/...' '-coverprofile=coverage.plan.out' -timeout 120s`, and passed at **69.8%**. An earlier unquoted invocation reported 87.5%; it did not establish the intended library-wide denominator and is superseded by the explicitly quoted run. The coverage figure is not branch coverage, capture coverage, or a new race-test result.

## 3. Sources and evidence model

Reference submodule: `reference/python-ring-doorbell`, pinned to `486193a80e7c924a0ab14b04d47305e1b36e419e`. Normal Go consumers must not need Python or initialize this submodule. Only the reference-comparison job needs it.

Assign independent evidence and implementation statuses. For example, a behavior can be `observed in capture` and `not implemented`; a passing synthetic test does not change its evidence source.

| Evidence code | Meaning | Requirements |
|---|---|---|
| C1 | New native mitmproxy recording | Actual sanitized fixture and schema; source conversation identified in prose |
| P1 | Pinned Python source or tests | Commit, file, test/function name; distinguish source behavior from tested behavior |
| L1 | Existing Go fixtures | Preserve their documented unknown capture dates/provenance |
| S1 | Synthetic robustness scenario | State the mutation and its parent fixture; never label captured |
| V1 | Explicit live verification | Date, device family, operation and environment; optional and separate |

Implementation statuses: planned, implemented, replay-tested, experimental, deprecated. Device support should identify feature, family/model evidence, account role, region when known, last verification, and known restrictions. Unknown is a valid value.

Maintain the support mapping in `docs/parity-matrix.md` and `docs/porting-progress.md`. Each operation links Go method, wire operation ID, Python reference, actual fixture files, tests, unsupported cases, and unresolved conflicts. Do not add a capture manifest or metadata registry. Field-level claims such as nullability or requiredness need evidence too. Never infer that a response field is universally required because one capture contains it.

### What the new recording actually adds

The supplied file is **native mitmproxy**, not a HAR: `docs/internal/network-capture-flows-ring.mitmproxy`. Parsing found 512 HTTP flows, including three WebSocket upgrades at `api.prod.signalling.ring.devices.a2z.com/ws`. Flows 2, 21 and 402 contain respectively 0, 254 and 243 WebSocket messages: **497 messages total**. Ordinals are one-based within the native file; message ordinals are one-based within each flow. The initial inventory incorrectly filtered on `ring.com` hostnames and missed these conversations; this corrected inventory supersedes that finding. Discovery must enumerate all hosts before classifying their roles. Counts are occurrences, not distinct features or devices.

| C1 observation on api.ring.com | Count/status | What to investigate or implement |
|---|---|---|
| GET `/device_info/v3/devices` | 16 / 200 | Already used by Go; strengthen mapping/replay and compare to Python legacy discovery |
| GET `/device_info/v3/devices/{id}` | 42 / 200 | Detail shape, capability fields, missing/null values |
| GET `/location_info/v3/locations` and `/location_info/v4/locations/{id}` | 3 and 8 / 200 | Location model and linkage; do not merge version schemas blindly |
| GET `/evm/v3/history/devices` | 6 / 200 | Account/device history shape and pagination evidence |
| GET `/evm/v2/timeline/devices/{id}` | 38 / 200 | Timeline semantics and mapping to public recordings/events |
| GET `/groups/v1/locations/{id}/groups` and `/groups/v1/locations/{id}/devices` | 34 and 10 / 200 | Groups are corroborated; compare exact routes to Python |
| GET/PATCH `/devices/v1/devices/{id}/settings` | 3 and 7 / 200 | Typed settings reads/updates after request-body classification |
| PUT `/clients_api/doorbots/{id}/siren_on` and `siren_off` | 1 each / 200 | New Go siren control candidate, with Python corroboration |
| PATCH `/commands/v1/devices/{id}` | 1 / 204 | Flow 178: `command_name: reboot`; separate HTTP device command |
| PUT `/duos/v1/devices/{id}/update` | 2 / 204 | Flows 349/352: `entity.live_view_enabled` false/true; separate settings operation |
| PUT `/clients_api/doorbots/{id}` | 1 / 204 | Compare legacy update body to current controls |
| PUT `/clients_api/dings/{id}/favorite` | 1 / 201 | Candidate recording favorite operation |
| DELETE `/clients_api/dings/{id}` | 1 / 204 | Candidate deletion operation; explicit opt-in live test only |

Five requests to `app-snaps.ring.com/snapshots/next/{id}` have no captured response. They establish that requests occurred, not a successful snapshot contract. Browser-proxied `account.ring.com/api/cgw/clients_api/ring_devices` must not be treated as proof of direct API authentication or routing.

C1 directly supports RTC signaling, PTZ and push-subscription shapes as detailed below. Three successful GET requests to `prd-api-us.prd.rings.solutions/api/v1/clap/tickets` provide bootstrap-route evidence; inspect their sanitized response fields and handshake relationship before equating this route with the existing Python/Go ticket request. No OAuth token exchange was identified; the already-supported auth mechanism stays as is. Existing fixtures remain L1 even when their names resemble Python fixtures. App telemetry, shopping, billing, support, and Neighbors GraphQL traffic are out of SDK scope for this iteration.

### Captured PTZ and signaling contract

| Nested RPC operation | Direction/count | Example evidence |
|---|---|---|
| `PTZ.Pan.Step` | Send / 21 | C1 flow 21, message 30 |
| `PTZ.Pan.Continuous` | Send / 16 | C1 flow 21, message 64 |
| `PTZ.Tilt.Step` | Send / 5 | C1 flow 402, message 14 |
| `PTZ.Tilt.Continuous` | Send / 6 | C1 flow 402, message 16 |
| JSON-RPC result | Receive / 48 | C1 flow 21, message 31 |
| `PTZ.Pan.Halted` | Receive / 2 | C1 flow 21, message 92; flow 402, message 197 |

The outer message contains `method: rpc`, `dialog_id`, `riid`, and a body containing `doorbot_id`, `session_id`, and `command`. The nested command contains `jsonrpc: "2.0"`, `id`, `method`, and `params`. Step parameters include direction, sessionId, timestamp and version; continuous commands also include speed. Captured directions are LEFT/RIGHT for pan and UP/DOWN for tilt. Observed continuous speeds include 0.5 and 0.0; zero-speed sends are the captured stop pattern. Do not infer the full accepted speed range, physical speed units, zoom support, or a separate stop RPC from these samples.

All 48 outgoing RPC commands have a `params.sessionId` distinct from outer `body.session_id`, and a command ID distinct from `dialog_id`. Preserve these identity domains; determine the control-session ID creation/lifetime from the conversation rather than substituting a signaling ID. Replies contain command ID and result fields sessionId/timestamp/version. Halted messages carry a method and params including reason `LIMIT_REACHED`; they are unsolicited events, not ordinary command results. Device timestamps differ from client timestamps: keep source values and do not compare them as a common wall clock without evidence.

The same capture includes live_view, playback, SDP, ICE, session_created, activate_session, camera_started, camera_options, mic_enable, stream_options, close, 74 outgoing pings and 74 incoming pongs. Push subscribe/ack, push_heartbeat, push_event and unsubscribe are also present. Eleven SDP responses specify `session_info.ping_interval: 10`; median per-session ping spacing is approximately 10 seconds, supporting seconds for this captured profile. Push subscription heartbeat and live-view session heartbeat are separate lifecycles even when sharing a socket. See the session design for precise timer policies and the requested 60-minute session maximum.

If a separate HAR is supplied later, assign it C2 and compare its actual requests, responses, and conversations to C1. A HAR export may lose native WebSocket information; retain native flow provenance and report missing bodies/frames explicitly.

### Capture-to-fixture pipeline

1. Read the raw capture locally; never replay it against production. Keep the original untracked and add precise ignore rules before committing capture work.
2. Produce a sanitized inventory of method, host, templated path, status, body format and flow ordinal. Do not output headers, query values, media or response bodies to logs.
3. Select operation-focused conversations, including prerequisite requests and before/after reads when present. Preserve ordering and meaningful status/body distinctions.
4. Redact credentials, cookies, signed URLs, personal data, addresses, coordinates, device/network identifiers, media and SDP/ICE network details. Use consistent synthetic mappings so cross-request identity still works; preserve string-vs-number and null-vs-absent distinctions.
5. Store the actual sanitized request/response and ordered session files with their JSON schemas. Do not add manifests, digests, format versions, capture timestamps, environment labels, or extraction metadata. Fail closed on unhandled formats; decode compressed/base64 bodies before inspection. Secret scanning complements review and cannot prove anonymization alone.
6. Generate synthetic errors as explicit test mutations of named fixtures. Explain the mutation in the test; public fixtures must contain no dependency on the local raw file. Keep protocol timestamps and version fields when they are part of the actual wire payload.

Suggested layout: `test/fixtures/{legacy,python,capture-c1,synthetic}/`, `test/replay/scenarios/`, `tools/capture/`, `docs/evidence/`. Adapt rather than immediately relocating all existing fixtures.

## 4. Target architecture

```text
Application
  -> pkg/ring: public client, request types, explicit session handles
  -> pkg/ringapimodels: stable domain responses, capabilities, errors
       -> internal mapping/services
            -> internal/httpapi: authentication, REST and HTTP device RPC
            -> internal/signaling: connection owner, reader/writer, message routing
                 -> RTC/session: SDP/ICE, keepalive, PTZ RPC, session controls
            -> internal/events: separately verified push/event transport
                 -> injected HTTP client, WS dialer, token getter

api/openapi.yaml + api/asyncapi.yaml + api/schemas/
  -> generated internal wire types and validation
  -> sanitized replay suite + normalized Python comparison
```

Keep raw schema versions inside adapters. Use a generic device identity plus capability support states (`supported`, `unsupported`, `unknown`), with evidence-backed device-specific fields. Avoid treating an unfamiliar device family as an error or claiming a capability from its name alone. Preserve opaque IDs as strings publicly; validate any numeric conversion required by an endpoint.

Keep the existing authentication implementation and API. This project is not an auth migration: preserve current login, OTP, refresh and token injection behavior, documenting and regression-testing it as implemented. Any discovered auth defect is a separately scoped compatibility fix. New signaling/session code consumes the existing credential mechanism and must not introduce an independent login or token store.

Expose injection through the existing HTTP option plus a narrow context-aware WebSocket dialer/connection abstraction. Endpoint constants/configuration belong in one internal location with test overrides. Clock and ID generation are internal test seams, not obligatory consumer configuration. Logging is optional and quiet by default; use redacted `slog` events if needed. Applications can instrument their HTTP transport.

Public concrete types are the primary API. Replace the large `ClientInterface` in the breaking release with small consumer-owned interfaces shown in examples. Session operations belong on DeviceSession, connection/subscription management on SignalingConnection, and HTTP operations on Client.

## 5. Proposed Go API and compatibility

Keep package organization purposeful, but allow new names and signatures where they clarify the feature model. Consolidate the public API index in `api.go`/`doc.go`. Publish a breaking migration table and use the appropriate release/module version. Existing authentication mechanisms are outside the redesign.

```go
// Target additions; not implemented by this plan.
type GetDevicesRequest struct { LocationID string }
type GetDevicesResponse struct { Devices []DeviceSummary }
type SetSirenRequest struct { DeviceID string; Enabled bool }
type SetInHomeChimeSettingsRequest struct {
    DeviceID string
    Enabled *bool       // nil means leave unchanged
    Kind *ChimeKind
    Duration *time.Duration
}

func (c *Client) GetDevices(context.Context, GetDevicesRequest) (*GetDevicesResponse, error)
func (c *Client) SetSiren(context.Context, SetSirenRequest) error
func (c *Client) SetInHomeChimeSettings(context.Context, SetInHomeChimeSettingsRequest) error

func (c *Client) OpenSignaling(context.Context, OpenSignalingRequest) (*SignalingConnection, error)
func (c *SignalingConnection) StartDeviceSession(context.Context, StartDeviceSessionRequest) (*DeviceSession, error)
func (s *DeviceSession) Answer() SessionDescription
func (s *DeviceSession) Wait(context.Context) error
func (s *DeviceSession) State() SessionState
func (s *DeviceSession) SendICE(context.Context, ICECandidateRequest) error
func (s *DeviceSession) PanStep(context.Context, PanStepRequest) (*PTZResult, error)
func (s *DeviceSession) TiltStep(context.Context, TiltStepRequest) (*PTZResult, error)
func (s *DeviceSession) PanContinuous(context.Context, PanContinuousRequest) (*PTZResult, error)
func (s *DeviceSession) TiltContinuous(context.Context, TiltContinuousRequest) (*PTZResult, error)
func (s *DeviceSession) StopPTZ(context.Context, StopPTZRequest) (*PTZResult, error)
func (s *DeviceSession) SetMicrophone(context.Context, SetMicrophoneRequest) error
func (s *DeviceSession) SetStreamOptions(context.Context, SetStreamOptionsRequest) error
func (s *DeviceSession) Receive(context.Context) (*SessionEvent, error)
func (s *DeviceSession) Close() error
```

### Session implementation shape

Replace `RTCStream` with `DeviceSession`, created through an explicitly owned `SignalingConnection`. The session supports SDP/ICE, PTZ and media controls; it is not merely a video stream. A WebSocket dialer is only the transport seam underneath it. Keep the session usable between commands through automatic keepalive, with a hard 60-minute session-age limit. See [session-design.md](session-design.md) for SDP profiles, ownership, timing and migration.

The handle owns device identity, dialog identity, signaling session identity, separate PTZ control-session identity, capability state, pending RPC requests, heartbeat deadlines and terminal status. Request structs take typed direction (and speed for continuous movement); IDs, version and timestamps are injected internally. `StopPTZRequest` identifies an axis; the implementation uses its tracked direction and the captured zero-speed command. Missing movement state must be handled explicitly. `PTZResult` denotes RPC acknowledgement, not proof of physical positioning; position/limit events have separate types. Zoom remains unverified.

Implementation components:

1. A connection owner with exactly one reader and one serialized writer. Demultiplex SDP/ICE, replies, session events and subscription traffic by their appropriate identities. Captured multiple conversations on one socket require isolation tests; public socket sharing is not necessary to expose.
2. A session state machine that completes offer/answer and activation prerequisites before allowing PTZ. `StartDeviceSession` uses its context for the returned session lifetime, bounded by the 60-minute maximum. Individual command contexts only bound that command; canceling one must not tear down other commands or the session.
3. A pending-RPC map populated before sending, keyed by nested command ID and scoped to the owning session. Match results/errors without blocking the reader; remove entries on completion/cancel/timeout and discard or report late replies without matching a new command. Validate dialog/session association. Treat Halted as an event.
4. An automatic keepalive worker that sends application ping and tracks pong deadlines while callers are idle. Validate negotiated interval units/ranges; use a documented fallback only if no valid interval exists. Socket ping/pong and subscription heartbeat are different mechanisms. Missed heartbeat or terminal I/O error fails the session and all pending commands.
5. A bounded send queue and bounded event queue. Heartbeats and stop/close must not starve behind repeated movement commands. Use deadlines and explicit backpressure errors; callbacks never run on the socket reader. Avoid a public raw `Send(map[string]any)` as the primary API.
6. Shutdown stops accepting commands, attempts a bounded stop for tracked continuous movement while the connection is usable, sends the evidenced session close, cancels workers, and resolves pending calls. Neither disconnect nor successful socket write guarantees that the camera stopped; surface unknown outcome. No automatic retry/replay of movement on reconnect. New connection means new session identities and explicit caller restart.

Receiving an SDP answer, PTZ result, or heartbeat must not consume messages intended for another API call. Keep a single demultiplexing reader; `Receive` reads the session event queue, never the socket directly. `Wait` exposes the terminal error and `State` exposes the lifecycle without requiring callers to manage goroutines or ping loops.

Use `GetDevices` as an additive unified view; retain `ListDevices` and its family-grouped response until a versioned migration. Final type placement is in ring/ringapimodels as appropriate, with aliases if necessary. Mutation methods returning only `error` are suitable when there is no meaningful response; do not invent empty response objects. Replace untyped settings maps with additive typed operations before deprecating legacy ones.

Document and test these contracts:

- Every network operation accepts context; caller cancellation interrupts blocked I/O and is discoverable with `errors.Is`.
- New clients do not start background activity until an operation needs it. Configuration changes after concurrent use are unsupported; deprecate mutable `Apply` usage in favor of construction options.
- The client owns a registry of sessions it creates. `Client.Close` closes them, waits for workers to exit, is idempotent, and rejects new operations. Caller-provided shared HTTP transports remain caller-owned.
- Remove the no-op `StopRTCStream` in the breaking API; callers close DeviceSession. Cover this migration explicitly rather than retaining success without an effect.
- A signaling session owns its read loop, serialized writes, heartbeat, cancellation and terminal error. `Wait` reports termination. Closing from a callback or peer-close handler must not deadlock by joining itself.
- RTC signaling is distinct from media reception. State explicitly whether the SDK only exchanges SDP/ICE or also owns a Pion peer connection; the initial contract should keep caller-provided SDP/media ownership.
- Event consumers receive typed normalized events through a bounded API. Default overflow should terminate with an explicit error rather than silently discard events. Delivery/order/reconnection guarantees depend on verified transport behavior.
- Unknown protocol fields are tolerated; malformed required fields yield typed protocol errors. No unchecked assertions on untrusted JSON.
- Streamed recording responses expose ownership of the body; callers must close it. Protect credentials across redirects to media hosts.

Unify error metadata (operation, status/code, retry-after, wrapped cause) while preserving existing concrete error types and `errors.As` behavior. Cover 2FA-required, unauthorized, forbidden, unsupported, not-found, rate-limited, malformed protocol, closed-session, transport and timeout errors. Avoid including raw server bodies or tokens in error text.

## 6. Protocol documents and generation

These documents describe observed **upstream Ring protocols**. They are not a REST server implemented by go-ring and are not an official Ring specification.

### OpenAPI

Create `api/openapi.yaml`, using a pinned OpenAPI 3.1 version supported by the chosen validator/generator. Specify verified hosts, path/query parameters, authentication/session prerequisites, request bodies, successful responses including empty 204 responses, known errors, and binary/redirect recording behavior. Keep legacy and newer routes distinct, mapping both to public methods where appropriate.

Include the C1 HTTP ticket/bootstrap route and existing supported auth routes unchanged in behavior. Cross-link the resulting stateful signaling/PTZ API to AsyncAPI. The captured PTZ commands are WebSocket messages, so do not invent HTTP `/ptz` endpoints in OpenAPI. The overall operation inventory must still list every PTZ command and link its AsyncAPI operation, so they remain visible from the API overview.

Each operation/schema carries `x-evidence` links, implementation status, and sanitized examples. Enumerate observed statuses; synthetic errors must be marked as test policy, not observed service behavior. Use nullable/optional properties accurately and allow unknown fields where the service is extensible. Document unknown pagination, rate limits, regional behavior, and CORS as unknown instead of making up defaults. This server-side Go SDK makes no browser CORS promise.

### AsyncAPI and RPC

Create `api/asyncapi.yaml` with a pinned AsyncAPI 3.x version, describing go-ring's send/receive perspective, actual WebSocket handshake, authentication ticket acquisition reference, message direction, correlation and session identifiers, error/close messages, and JSON schemas. Keep session-state transitions in `docs/session-design.md` alongside the machine-readable schema.

The signaling envelope contains `method`, `dialog_id`, and `body`, with `riid` present in captured messages; the outer envelope is not JSON-RPC 2.0. Its `rpc` body embeds an actual JSON-RPC 2.0 command. Define separate reusable schemas for outer routing, PTZ requests, result replies, errors (synthetic until observed), and unsolicited Halted messages. Include all four captured PTZ request methods, their result correlation, and Pan.Halted, plus live_view, playback, session_created, sdp, ice, activate_session, notification, camera_options, camera_started, mic_enable, stream_options, ping, pong and close. Model push subscribe/ack, heartbeat, events and unsubscribe separately. Distinguish application ping/pong from WebSocket control frames; do not infer request/reply pairs for every message.

Python intercom tests separately show JSON-RPC 2.0 carried over HTTP `device_rpc`. Describe that under OpenAPI with shared RPC schemas for method/params/id/result/error. Do not combine it with RTC WebSocket signaling or claim the Go event WebSocket matches Python's FCM push listener.

Proposed RTC lifecycle: new -> ticket requested -> connecting -> negotiating -> active -> closing -> closed/failed. Test allowed order variants, failures at every transition, correlation mismatch and late messages. Reconnection or session resumption is unsupported until deliberately implemented and documented.

Generate internal wire DTOs and optionally the thin HTTP client; keep the domain API and RTC state machine handwritten. First run a tooling spike for schema compatibility. Pin generators, separate generated files, and fail CI on regeneration diffs. Schema validation and replay tests must be independent enough that generated code cannot validate itself using only generated fixtures.

## 7. Test inventory and Python mapping

Use table-driven Go tests for protocol cases, local HTTP/WebSocket servers for lifecycles, and fuzz tests for parsing. Default CI must make no vendor requests and require no credentials. Block unexpected hosts and fail unexpected replay requests.

| Work package | Python evidence | Go tests to implement or strengthen |
|---|---|---|
| Authentication/session | `auth.py`, auth/session fixtures and setup in `tests/conftest.py` | 2FA challenge, wrong/expired OTP, refresh rotation, token precedence, session registration once under concurrency, retry after failed registration, 401/403/429, cancellation; no implicit auth retry |
| Devices/health | `test_ring.py`: basic/chime/doorbell/shared/stickup attributes | All families, shared permissions, unknown family, missing/null/zero values, large IDs, empty account, partial response, health mapping, C1 v3 vs legacy normalization |
| Controls | `test_stickup_cam_controls`, motion detection, existing doorbell type | Exact endpoint/method/body, validation bounds, nil vs false/zero, empty 204, unsupported capability, permission failures; C1 settings/siren cases |
| Groups/intercom | `test_light_groups`, `test_other.py` attributes/controls/invitations/open-door | Explicit parity backlog; schema and fixture work before adding APIs; JSON-RPC ID/result/error handling; no live unlock tests by default |
| History/recordings | Existing Go tests, Python history fixtures and implementation | Pagination, filters, UTC/timezones, empty history, large IDs, binary read/close, truncated stream, expired/signed redirects, cancellation; C1 history/timeline mapping |
| Push notifications | `test_listen.py`: listen, active dings, ding expiry, subscription/GCM/FCM failures; v1/v2 fixtures | Normalize both payload generations, deduplicate by evidenced identity, fake-clock expiry, bounded consumer behavior; separate transport-adapter tests |
| RTC | `webrtcstream.py`; existing Go RTC tests | Ticket/auth failure, handshake rejection, missing answer, close-before-answer, ICE before/after session ID, wrong dialog, malformed/unknown messages, multi-section SDP, heartbeat loss, read/write failure, concurrent send/close, caller/client shutdown |
| Stateful PTZ/controls | C1 flows 21/402; new Go cases | Pan/tilt step and continuous RPC, zero-speed stop, separate identity domains, matched results, LIMIT_REACHED events, media controls, interleaved SDP/ICE/ping/pong, idle-session keepalive, concurrent requests and out-of-order replies, late/duplicate/wrong-session replies, command cancellation, bounded queues, shutdown while moving; no zoom claim |
| Transport/errors | Existing Go transport tests plus S1 cases | Query encoding including repeated keys, headers, body closure, non-JSON errors, content types, oversized/truncated bodies, wrapped context errors, no unintended retries |
| Capture tooling | C1 plus deliberately planted synthetic secrets | Deterministic output, referential integrity, redaction of all sensitive locations, compressed/base64 handling, missing-response representation, secret scan |

Python sync-wrapper tests do not need direct Go equivalents. Carry their behavioral intent only where relevant. The inspected top-level Python tests do not provide a dedicated RTC test file; RTC source and existing Go tests need separately labeled evidence.

### Shared replay and differential tests

Build a language-neutral scenario format: ID, evidence, input operation, ordered or explicitly unordered exchanges, request matchers, response/error/delay, expected normalized result, expected side effects, and allowed nondeterminism. Capture fixtures supply exchanges; handwritten expected values supply the oracle.

Use shared scenarios where behavior is comparable through Go and an external Python adapter beside the pinned submodule. Redirect Python transport to a local harness and compare normalized devices, histories, error categories and request effects. Python comparison is advisory, not an equality gate. Freeze time/UUIDs and isolate dependencies. Run unsupported/new C1 routes as Go-only contract tests. Record intentional divergences with the selected behavior, working implementation/capture evidence, scope and regression tests; these are accepted outcomes, not failures to fix by imitating Python.

Strengthen the Go mock first: full host/method/path/query/header/body matching, independent response streams, exchange consumption checks, deterministic errors, and failure on unexpected calls. Assert actual outbound RTC frames and terminal errors; merely waiting and checking a non-nil stream is insufficient.

### Coverage and release gates

- Library statement coverage >=90% overall, with per-package baselines that cannot regress. Include maintained handwritten code under `pkg` and future `internal`; exclude examples, test helpers and generated code explicitly and report exclusions.
- Publish both raw total and handwritten coverage if code generation materially changes the denominator. Do not count generated getters to meet the target.
- Maintain a behavioral matrix in addition to percentages: every stable method has happy, invalid-input, upstream-error and cancellation cases where applicable; every session transition has failure/cleanup tests.
- Run existing supported OS/Go matrix, race detector, vet, formatting and example builds. Use deterministic peer synchronization; add focused leak/termination assertions rather than fragile global goroutine counts.
- Add bounded CI fuzz smoke tests for device/error/RTC/SDP decoders, with longer scheduled fuzzing optional. New failures become regression corpus cases.
- Pin and validate OpenAPI/AsyncAPI tools; check examples and source-to-fixture links; require clean generation. Coverage upload is informational; the local threshold fails independently of Codecov availability.
- Live tests stay build-tagged and opt-in. Separate read-only checks from mutations, sirens, recording deletion, and access-control actions. No raw capture replay against live devices.

## 8. Exact README shape and supporting documentation

The README should be short enough to get a consumer running, then direct them to detailed contracts. Use this order and wording as the editorial template; populate claims from the evidence registry at release time.

```markdown
# go-ring
[CI] [Go version] [Go reference] [Release] [Coverage] [License]

go-ring is an unofficial Go client for Ring devices, recordings, controls,
and live-view signaling. It provides context-aware operations and lets your
application supply credentials and network clients.

Ring's upstream interfaces are undocumented and can change. The support
matrix distinguishes reference-backed, captured, and live-verified behavior.

## Install
go get github.com/portpowered/go-ring@<current-tested-release>
[Minimum Go version and compatibility policy]

## Quick start
[Complete, compiled example: context timeout, injected token, list devices,
error handling, client.Close. Never print credentials or full device data.]

## Authentication
[Existing supported token injection/login/2FA/refresh APIs, unchanged.
Link to a complete auth example and document current token precedence.]

## Supported devices and features
[Generated matrix: feature | device family | implementation status |
evidence | limitations. Separate signaling, media, and push events.]

## Common operations
[Links to examples: health, typed controls, recording download, live session]

## Stateful live view and PTZ
[Open session once; negotiate SDP/ICE; pan/tilt on the same handle; receive
results/events; stop movement and close. Keepalive is automatic. Link to
AsyncAPI and explain command acknowledgement vs physical completion.]

## Errors and operation lifetime
[errors.Is/As example, cancellation, closing recording bodies and sessions]

## Configure networking
[HTTP client, WS dialer, token getter; retries and observability are opt-in]

## API and architecture
[Go reference] [OpenAPI] [AsyncAPI] [Upstream protocols] [Library design]

## Evidence and testing
[Python pin] [Capture provenance] [Test matrix] [How to run offline tests]

## Compatibility and contributing
[Support policy] [Migration guide] [Contributing] [Security reporting]

## License and acknowledgements
[Project license] [Reference-project license and provenance notices]
```

The release placeholder must be replaced before publication. Keep the working list-devices example as the starting point. Move detailed request/response definitions to Go documentation and remove the broken auth snippet. Build examples in CI and use tested example functions for README snippets; do not execute live examples in CI. Use existing coverage services/badges instead of building custom badge rendering.

| Document | Required content |
|---|---|
| Topic in the original outline | Current documentation and coverage |
|---|---|
| Architecture and protocol overview (`docs/architecture.md`, `docs/protocols/overview.md`) | [Architecture](architecture.md), [HTTP protocol](protocols/http.md), [parity matrix](parity-matrix.md), and [session design](session-design.md) cover ownership, API families, hosts/versions, and known unknowns. These consolidated documents are the maintained equivalents; no separate overview is needed. |
| Authentication (`docs/auth.md`) | [Authentication](auth.md) documents token precedence, hardware identity, 2FA, explicit refresh, storage ownership, and credential safety. |
| RTC/session design (`docs/rtcstream.md`, `docs/protocols/rtc.md`) | [Session design](session-design.md) is the current connection/session contract; [legacy RTC streaming](rtcstream.md) describes the older API. AsyncAPI holds wire schemas. A separate `protocols/rtc.md` would duplicate the session design. |
| Errors (`docs/errors.md`) | [HTTP error boundaries](protocols/http.md#error-boundaries), [migration behavior](migration.md), and the README's request-handling guidance cover typed errors, refresh, permission failures, and uncertain mutations. |
| Networking (`docs/networking.md`) | [Configuration ownership](constants-and-configuration.md), [HTTP contracts](protocols/http.md), [HTTP test progress](http-test-progress.md), and [migration behavior](migration.md) cover injected clients/endpoints, response handling, bounded retries, contexts, and redirect limitations. Proxy/TLS behavior follows the injected Go clients; no separate retry-after feature is claimed. |
| Support (`docs/support.md`) | The reviewed [parity matrix](parity-matrix.md), [porting progress](porting-progress.md), and README evidence table list supported behavior and explicit unknowns; they are not generated device-certification claims. |
| Testing (`docs/testing.md`) | [Replay format](replay-format.md), the [reverse-engineering process](internal/process-of-reverse-engineering.md), this plan's coverage denominator, and `tools/verify_reference.py` describe offline checks, Python mapping, deterministic seams, and opt-in live tests. |
| Evidence (`docs/evidence/README.md`) | [Recording notes](../test/recordings/README.md), [legacy fixture notes](../test/fixtures/README.md), and the [parity matrix](parity-matrix.md) describe capture scope, source distinctions, synthetic fixtures, and known conflicts. No separate evidence registry is maintained. |
| Reverse engineering (`docs/reverse-engineering.md`) | The [reverse-engineering process](internal/process-of-reverse-engineering.md), [recording notes](../test/recordings/README.md), and `tools/capture/extract.py` describe local extraction and sanitization. `CONTRIBUTING.md` covers source attribution and review. |
| Migration (`docs/migration.md`) | [Migration guidance](migration.md) covers current lifecycle and behavior changes; feature-specific settings and identity details remain in their API docs. |
| Repository instructions (`AGENTS.md`) | No project-root AGENTS.md is maintained. [README](../README.md), [contributing guide](../CONTRIBUTING.md), and the [reverse-engineering process](internal/process-of-reverse-engineering.md) provide user intent, Go contribution practice, evidence rules, and verification commands. |

Required handling guidance: a timed-out mutation may already have succeeded; reconcile current state before deciding to retry. Automatic retries are limited to GET/HEAD, at most three attempts, and stop when the request context is canceled; they do not honor `Retry-After`. A 401 may call for one caller-driven `RefreshToken` and token persistence, but the client does not run an automatic login loop. A 403 is an authorization/permission failure, not evidence that the device is missing. Do not automatically fall back between endpoint generations for arbitrary errors. Experimental event disconnection can lose events because recovery semantics are not verified. Unknown device capabilities remain unknown rather than being silently reported as false.

## 9. Sequenced implementation tasks

| Task | Deliverable | Depends on | Acceptance |
|---|---|---|---|
| T0 Baseline/reference | Pin Python, record tests/API/coverage, map compatibility and third-party notices | None | Reproducible baseline and file/test mapping; reference remains unmodified |
| T1 Evidence ingestion | Ignore raw capture, parser/sanitizer, C1 inventory, operation matrix, focused fixtures | T0 | Repeatable sanitized output; reviewed provenance; no raw secrets in tracked files |
| T2 Replay infrastructure | Strict transport, scripted WS peer, fake clock/IDs, common scenario schema | T0 | Unexpected request fails; fresh response bodies; no real network; no sleep-dependent protocol assertions |
| T3 Public/lifecycle contracts | Client/SignalingConnection/DeviceSession APIs, ownership, RPC correlation, keepalive, SDP profiles, 60-minute expiry; preserve auth | T0, T2 | Breaking migration is explicit; close drains sessions/pending RPC; fake-clock expiry passes; auth unchanged |
| T4 Protocol specifications | OpenAPI, AsyncAPI, shared schemas, lifecycle notes, generation spike | T1, T3 | Valid specs with evidence links, correct transport separation and deterministic generation |
| T5 Core reliability | Existing-auth regression tests; resolve divergent raw routes in parity matrix; devices/controls/recordings and capabilities | T2–T4 | Auth unchanged, method/route/body parity verified, migration examples compile, documented failures covered |
| T6 New captured behavior | Device/history version adapters, settings, siren; separately decide favorite/delete/groups | T1, T4, T5 | Each addition has body-level evidence, typed API, replay and negative tests; no scope from telemetry |
| T7 Stateful signaling/PTZ | Persistent session, automatic heartbeat, SDP/ICE, four PTZ commands/results/halt events, media controls; separately scope captured push subscriptions | T2–T4 | C1 sessions replay with PTZ/keepalive interleaving; cancellation/correlation/stop/close tests pass; all commands linked in AsyncAPI and API overview |
| T8 Python comparison | Adapter, normalized corpus, advisory parity/divergence report | T2, T5 | Comparable cases replay offline; intentional capture/working-Go-backed divergences accepted without Python equality gate |
| T9 Documentation/release | README, examples, support/migration docs, coverage/contract CI gates | T5–T8 | >=90% handwritten library coverage; public examples compile; support claims traceable |

T6 additions are not required to make existing APIs reliable. Do not delay lifecycle fixes until every capture route is implemented. A stable release may retain explicitly experimental event support while listing it outside stable guarantees.

Before copying Python source or fixtures, record their license and attribution in the provenance review; the reference has an LGPL-3.0 license, while this project declares Apache-2.0. Keeping the reference submodule is separate from incorporating its contents into the Go implementation. Prefer independently authored behavior tests and document the provenance of anything reused.

Completion means dependable existing APIs, a published contract, reproducible evidence-backed tests, passing CI gates, and an honest support matrix. It does not mean every Ring endpoint is supported or that a high statement percentage certifies live compatibility.

## References

- [Pinned Python project](https://github.com/python-ring-doorbell/python-ring-doorbell/tree/486193a80e7c924a0ab14b04d47305e1b36e419e)
- [Python test setup](https://github.com/python-ring-doorbell/python-ring-doorbell/blob/486193a80e7c924a0ab14b04d47305e1b36e419e/tests/conftest.py), [device tests](https://github.com/python-ring-doorbell/python-ring-doorbell/blob/486193a80e7c924a0ab14b04d47305e1b36e419e/tests/test_ring.py), [push tests](https://github.com/python-ring-doorbell/python-ring-doorbell/blob/486193a80e7c924a0ab14b04d47305e1b36e419e/tests/test_listen.py)
- [Python RTC source](https://github.com/python-ring-doorbell/python-ring-doorbell/blob/486193a80e7c924a0ab14b04d47305e1b36e419e/ring_doorbell/webrtcstream.py), [intercom tests](https://github.com/python-ring-doorbell/python-ring-doorbell/blob/486193a80e7c924a0ab14b04d47305e1b36e419e/tests/test_other.py)
- [OpenAPI 3.1.1 specification](https://spec.openapis.org/oas/v3.1.1.html), [AsyncAPI 3.0 specification](https://www.asyncapi.com/docs/reference/specification/v3.0.0), [mitmproxy features and HAR export](https://docs.mitmproxy.org/stable/overview/features/)

## Error handling compatibility

The `ringapimodels.Is...Error` helpers inspect wrapped errors, so adding caller
context with `fmt.Errorf("operation: %w", err)` preserves classification.
`errors.Is` retains cancellation causes and `errors.As` exposes typed details.
`HTTPError.Error()` includes status and an explicit message, but omits the raw
response body. `HTTPError.Body` remains available for deliberate diagnostic
inspection; callers must handle that data as sensitive.
