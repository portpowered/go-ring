# Constants and configuration ownership

Proposed implementation layout. Centralize protocol defaults without exposing internal endpoint versions as the consumer API.

| Location | Owns | Examples |
|---|---|---|
| `internal/protocol/endpoints.go` | Host/base URL defaults and named endpoint templates | OAuth host, Ring API host, Ring Solutions bootstrap host, signaling WebSocket URL; device/settings/history/ticket paths |
| `internal/protocol/profiles.go` | Explicit endpoint/protocol profiles | Legacy ticket POST vs captured tickets GET; version and region choices supported by evidence |
| `internal/protocol/signaling.go` | Wire method names and protocol constants | live_view, rpc, ping/pong, PTZ.Pan.Step, PTZ version; query parameter names |
| `internal/protocol/generated/` | Generated wire enums and DTOs from API schemas | Schema-generated request/response types; no handwritten duplicate enum values |
| `internal/signaling/policy.go` | Named SDK timer/queue defaults and validation | 60-minute session maximum, handshake/negotiation/RPC/pong/close policy; bounded queue defaults |
| Public `pkg/ring/options.go` | Consumer configuration | HTTP client, WebSocket dialer, logger, existing token options, validated shorter session duration |
| Public `pkg/ring/models.go` or `pkg/ringapimodels` | Stable consumer enums/value types | PanDirection, TiltDirection, SessionState, capability support state |
| `internal/testkit/replay/endpoints.go` | Local server profiles only | httptest origins and scripted WebSocket addresses; never used as production defaults |

`endpoints.go` exports only within internal consumers. Proposed `EndpointSet` fields: OAuthBaseURL, APIBaseURL, SolutionsBaseURL, SignalingURL. Use parsed URLs plus path/query builders, not string replacement of ticket values or device IDs. Represent a host and a path separately; origin overrides must apply consistently to auth/bootstrap, recordings and signaling. Do not place credentials, ticket values, client/session IDs, timestamps, signed media URLs or negotiated ping intervals in constants.

Endpoint values currently spread across `pkg/ringapimodels/constants.go` and client constructors move to this internal owner. The existing speculative event endpoint must be marked experimental or removed with the old event API, not silently made a stable default. Region-specific profiles require evidence; a captured US host does not establish global routing or authorize changing all users to that region. Retain working bootstrap behavior unless a tested change is justified.

Configuration precedence: explicit injected endpoint/profile > selected verified profile > shipped default. Negotiated per-session values (such as ping_interval) are runtime state validated against the profile; they do not overwrite package globals. Existing auth option precedence remains unchanged. For testing, expose one documented advanced endpoint override option if external-package tests need it; otherwise use an internal factory. Keep test-only clock/ID seams internal.

OpenAPI/AsyncAPI are the canonical wire schema sources; generated DTO/enums should come from them. For handwritten endpoint defaults, treat the endpoint registry as the runtime source and add a consistency test against the specifications' server/path/method declarations. Do not claim both files independently authoritative. SDK lifecycle defaults are policy, documented in session-design.md and tested against named policy values; they are not upstream guarantees.

No host literals or message strings scattered through feature methods. Feature code references named endpoint builders and wire constants. No guessed fallback hosts, transparent version changes, automatic credential forwarding to arbitrary redirects, or global mutable endpoint settings. Each client receives an immutable copied configuration; each session owns negotiated values. Changing a constant/profile requires linked evidence plus affected replay/spec tests.

## Current implementation

`internal/protocol/endpoints.go` is the runtime owner of OAuth, API, Solutions,
signaling, and experimental event hosts and the currently centralized route
constants. `internal/protocol/profiles.go` selects verified defaults;
`pkg/ring/endpoints.go` exposes `Region`, `Endpoints`, `WithRegion`, and
`WithEndpoints`. HTTP origins accept http/https and signaling accepts ws/wss;
userinfo and fragments are rejected. Configuration is per client. Explicit
endpoint overrides take precedence regardless of option order. US is the
default; EU/FE require an explicit Solutions origin for signaling bootstrap.

`internal/signaling/policy.go` owns the new session policy: 60-minute maximum
age, 30-second negotiation deadline, 10-second handshake/write/RPC limits,
2-second best-effort close, three missed heartbeat intervals, bounded queues,
and 1 MiB message/ticket limits. The negotiated interval remains session state.
The fallback heartbeat is five seconds; the recorded sessions advertise ten.
The clock seam is internal and the sixty-minute core lifetime is tested with
virtual time.

This is still a migration: generated DTOs, a complete wire-method registry,
and removal of every legacy literal are planned work. The table above is the
target ownership layout and does not claim those files already exist.
