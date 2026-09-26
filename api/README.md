# Schema value conventions

The model boundary is explicit: `openapi.yaml` generates `pkg/generatedhttp`
for Ring HTTP request and response bodies, and `asyncapi.yaml` generates
`pkg/generatedsignaling` for signaling frames. REST and signaling adapters
decode these wire models, then project them into SDK-facing types where needed.
`client-models.openapi.yaml` generates only the public
types in `pkg/ringapimodels`. Internal transport errors and recording body
ownership live below the public package, with public aliases for callers.
Closed public control choices (volume target and test sound)
also come from this schema and are used directly by public request fields.
The in-home chime update uses generated typed optional fields instead of an
arbitrary settings map. History kind stays an open string because the server
may introduce new recording kinds.

The legacy account-event WebSocket has no captured wire schema in this
repository and remains separate from the captured signaling protocol. Its
transport passes through decoded JSON; `pkg/ring` projects that data into the
public `Event` model.

The OpenAPI files describe captured HTTP responses and public SDK models.
`asyncapi.yaml` describes signaling frames. Each schema
names observed values without treating one recording as the complete set of
possible future vendor values.

Use `enum` or `const` where the client sends and validates a fixed value, such
as PTZ direction, RPC method, or SDP type. Use `x-extensible-enum` on an otherwise
ordinary `type: string` (or integer) for an observed value set that is **open**.
For example, `shoulder_tap` is a known notification type, while a new server
notification type must still decode. Generated Go models therefore keep those
fields as strings. `x-known-tokens` documents observed members of a
comma-separated query value; it is not a restriction on the full string.

Numeric `minimum` and `maximum` constraints are used only where the client
enforces the range or the wire meaning is established. Continuous PTZ command
speed is normalized from `0` (stop) through `1` (full requested speed), so
both the signaling schema and public Go methods reject values outside that
range. Device-reported `ptz_settings.*.movement.max_speed` uses the same
normalized scale but is a capability value, not a command. Other settings
whose units or upper bounds are unverified remain open with explicit
descriptions rather than guessed limits.

The observed values come from `tests/replay/fixtures/recordings`, while the
Python legacy fixtures establish additional device families and history kinds.
The schema contract tests check captured payloads, open-enum behavior, and
bounded invalid variants.

`Device.kind` and `Device.family` remain open strings on the wire. The named
`DeviceFamilyCode` and device-kind enums are a generated catalog of known
values used to select public device projections. Add new known hardware to
those OpenAPI enums, regenerate, and add a replay case. Unknown kinds retain
their raw identity and are exposed as generic devices; no prefix guess is made.
