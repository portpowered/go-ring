# Schema value conventions

The OpenAPI files describe captured HTTP responses, dependency projections, and
public SDK models. `asyncapi.yaml` describes signaling frames. Each schema
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
