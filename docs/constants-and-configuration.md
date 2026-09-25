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
