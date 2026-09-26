# Constants and configuration ownership

Keep service URLs, HTTP paths, WebSocket method names, RPC names, JSON field
names, and SDP directions in `internal/protocol`. Those values correspond to
OpenAPI, AsyncAPI, or recorded wire frames. A new wire literal needs a matching
schema or recording, plus a focused replay assertion.

Keep local lifecycle limits and queue sizes in `internal/signaling/policy.go`.
Keep REST-only parsing rules in `pkg/dependencies/rest/constants.go`. Public
defaults and enum values belong beside their public types or generated schema.
Callers can override endpoints through `ring.WithEndpoints`; this is needed
for replay peers and regional bootstrap configurations.

The linter rejects repeated string literals (`goconst`) and magic numbers in
arguments, conditions, cases, operations, and returns (`mnd`). Generated code,
tests, and examples are excluded from these rules. This is a guard against
new scattered values; it does not remove the named protocol and policy
constants that document the implementation.
