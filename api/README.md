# Schema value conventions

The canonical internal wire models live in `pkg/dependencymodels`. REST models
are generated from `openapi.yaml` into responsibility-group files under
`pkg/dependencymodels/rest`; `openapi.base.yaml` and the files in `schemas/`
compose that checked-in bundle. `make generate-api` regenerates the bundle and
models. Signaling frames are generated from `asyncapi.yaml` into
`pkg/dependencymodels/signaling`, and the pinned push receiver's HTTP exchanges
and Ring notification payloads are generated from `api/external/fcm.openapi.yaml`
into `pkg/dependencymodels/fcm`. REST, signaling, and push adapters decode
these wire models, then project them into SDK-facing types where needed.
`internal/generatedhttp`, `pkg/generatedhttp`, and their FCM equivalents are
generated client packages or compatibility aliases; they are not canonical
wire-model homes.
`client-models.openapi.yaml` generates only the public
types in `pkg/ringapimodels`. Internal transport errors and recording body
ownership live below the public package, with public aliases for callers.
Closed public control choices (volume target and test sound)
also come from this schema and are used directly by public request fields.
The in-home chime update uses generated typed optional fields instead of an
arbitrary settings map. History kind stays an open string because the server
may introduce new recording kinds.

The account-event WebSocket uses the extensible `AccountEventFrame` schema in
`asyncapi.yaml`. The transport preserves unknown event fields, and `pkg/ring`
projects the decoded frame into the public `Event` model.

The OpenAPI files describe HTTP responses from historical and synthetic
examples and public SDK models.
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

The observed values come from captured fixtures under `tests/replay/fixtures`.
Baseline fixtures with unknown capture provenance supply additional device
families and history kinds.
The schema contract tests check captured payloads, open-enum behavior, and
bounded invalid variants.

## HTTP reference evidence limits

The API reference describes what each operation does. Its operation text does
not indicate how each contract was established. In particular:

- OAuth authorization, credential submission, two-factor verification, and
  token exchange follow the current PKCE client behavior; no matching C1 HTTP
  capture establishes those exchanges. Session registration is also derived
  from client behavior rather than a C1 capture.
- Chime volume and doorbell control request shapes include synthetic replay
  cases. Whether the server requires their `description` query fields is
  unverified. Device health, the legacy doorbell history route and filters,
  and active dings have no matching C1 HTTP capture.
- Recording share playback and the legacy snapshot routes use synthetic replay
  cases. Recording stream redirects and media hosts are unverified. The
  captured app-snapshots route has a different, incomplete response shape.
- The captured GET location tickets route is distinct from the POST signaling
  bootstrap route, which is backed by client behavior and tests. Intercom
  unlock follows an external Ring client reference. Push registration follows
  a source-client contract and is absent from the camera capture.
- The settings patch contract includes fields beyond the public typed motion
  setting. Do not infer support for every extensible setting from one capture.

`Device.kind` and `Device.family` remain open strings on the wire. The named
`DeviceFamilyCode` and device-kind enums are a generated catalog of known
values used to select public device projections. Add new known hardware to
those OpenAPI enums, regenerate, and add a replay case. Unknown kinds retain
their raw identity and are exposed as generic devices; no prefix guess is made.
