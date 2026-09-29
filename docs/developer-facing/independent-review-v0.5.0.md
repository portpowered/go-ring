# Independent review: go-ring v0.5.0

**Reviewed implementation commit:** `916f23132b27b720e0aef95fa26eb5c63b52c809`

**Exact-SHA CI:** [run 36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097) — **SUCCESS, all 13 jobs passed**.

**Review date:** 2026-09-29

**Overall verdict:** **PASS.** All 15 checklist items are supported by the evidence below; findings F-1 through F-5 are resolved. The implementation SHA above is the reviewed code. The report, checklist link, and refreshed coverage note are documentation-only sign-off changes made after that SHA.

I independently reviewed all 15 entries in [`docs/template-checklist.md`](../template-checklist.md) and the rules in [`go.md`](../standards/go.md), [`schemas.md`](../standards/schemas.md), [`client-api.md`](../standards/client-api.md), and [`library.md`](../standards/library.md). I did not implement the reviewed code. Exact-SHA CI is green. A focused race run of the changed CLI and failure-replay tests passed twice. The independent report, checklist link, and refreshed coverage note are documentation-only sign-off changes; the implementation SHA above is unchanged.

## Checklist verdicts

### 1. Library independence — PASS

The public package, examples, README, and customer guides contain no consuming-application adapter or rollout plan. [`README.md`](../../README.md#L172) assigns peer creation and media rendering to the caller; [`library-architecture.md`](library-architecture.md#L1) describes the reusable library boundary and caller-owned WebRTC peer. This satisfies LIB-14.

### 2. Supported operations, authentication, errors, and transport injection — PASS

The supported-operation table in [`README.md`](../../README.md#L232) names the exported client methods. The MDX guides cover authentication and typed errors, device operations, and transport configuration; for example [`authentication.mdx`](../guides/authentication.mdx#L1), [`device-operations.mdx`](../guides/device-operations.mdx#L1), and [`client-configuration.mdx`](../guides/client-configuration.mdx#L1) use the current public API. [`index.mdx`](../guides/index.mdx#L29) and [`tests/replay/fixtures/README.md`](../../tests/replay/fixtures/README.md#L1) distinguish synthetic examples and historical files from provider evidence. This satisfies LIB-10–12 and API-14.

### 3. README badges and repository values — PASS

[`README.md`](../../README.md#L3) displays Go version, CI, replay coverage, release, Go Reference, license, and documentation badges using `portpowered/go-ring`. The coverage badge points to the published report, and the README's install and example links use the repository's actual module path. This satisfies LIB-15.

### 4. Generated API reference and wire contracts — PASS

The API source of truth is checked in as [`openapi.yaml`](../../api/openapi.yaml#L39), [`asyncapi.yaml`](../../api/asyncapi.yaml#L20), [`client-models.openapi.yaml`](../../api/client-models.openapi.yaml#L9), and separate external FCM/MCS contracts. The FCM/MCS inventory pins receiver version, commit, checksum, license, socket authority, TLS, frame encoding, tags, active messages, and dependency call site ([`push-protocol-inventory.yaml`](../../api/external/push-protocol-inventory.yaml#L1)); MCS constants are generated from it by [`generate_mcs.mjs`](../../tools/protocols/generate_mcs.mjs#L1). `tools/protocols` checks schema/model parity and generation contracts; `tools/routegate` binds routes, keys, channels, and MCS calls to actual call sites and includes negative mutation tests, including `TestMCSSourceGateRejectsContractMutations` and `TestMCSSourceGateRejectsExtraReceiverDial` in [`fcm_source_test.go`](../../tools/routegate/fcm_source_test.go#L359). The public projection exception is limited to behavior/custom-decoding adapters, with JSON field parity checked by `TestHandwrittenClientProjectionFieldsMatchSchema` ([`client_projection_contracts_test.go`](../../tools/protocols/client_projection_contracts_test.go#L15)), as allowed by SCHEMA-10. Exact-SHA schema-generation, fixture-contract, public-consumer, docs-site, API-compatibility, lint, coverage, and all six OS/Go matrix jobs passed in [CI run 36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097). The exact-SHA docs-site job rebuilt the rendered pages and passed its internal-link check.

### 5. Offline checks, all-rules lint, and persisted token keys — PASS

[`golangci.yml`](../../.golangci.yml#L11) sets the literal `default: all`; [`ci.yml`](../../.github/workflows/ci.yml#L184) installs golangci-lint v2.3.0 and runs blocking `make lint` for the root and CLI modules. Exact-SHA lint passed in CI. The token-file regression in [`diagnostic_cli_test.go`](../../tests/replay/diagnostic_cli_test.go#L160) asserts the exact persisted keys `access_token`, `expires_in`, `hardware_id`, `received_at`, `refresh_token`, and `token_type`. All six exact-SHA OS/Go race-matrix jobs passed, satisfying GO-15.

### 6. Synthetic fixtures and coverage — PASS

[`tools/coverage/main.go`](../../tools/coverage/main.go#L200) reports maintained `pkg` and `internal` code, explicitly excluding generated wire models, testkit, examples, tests, and tools; [`baselines.json`](../../tools/coverage/baselines.json#L1) enforces package floors. Exact-SHA CI reports replay coverage of 3,050/3,579 (85.22%), unit coverage of 2,476/3,579 (69.18%), and combined coverage of 3,340/3,579 (93.32%); combined package results are WebSocket 90.8%, REST 95.0%, push 88.7%, and `pkg/ring` 92.8% ([`coverage.md`](coverage.md#L1)). The exact-SHA coverage job passed all three suites and package floors. The separate profiles and generated-code exclusions are documented and enforced by the coverage tool.

### 7. Package boundaries and downstream consumer — PASS

The reusable API is in `pkg/ring`, public projections in `pkg/ringapimodels`, transport implementations under `pkg/dependencies`, and canonical wire models under `internal/generatedhttp`, `internal/generatedsignaling`, and `internal/generatedfcm`; [`library-architecture.md`](library-architecture.md#L43) documents those responsibilities. The [`public-consumer` job](../../.github/workflows/ci.yml#L65) builds a separate module importing the public packages without requiring the vendored push receiver as a separate module; that job passed on the reviewed SHA. This satisfies LIB-01–03 and SCHEMA-16.

### 8. Client construction and configuration — PASS

[`NewClient`](../../pkg/ring/client.go#L18) establishes defaults for region, user agent, endpoint origins, and transports before applying options. [`client_options.go`](../../pkg/ring/client_options.go#L14) validates endpoint and transport configuration, including nil HTTP/WebSocket inputs and HTTP cookie-jar rejection. Account credentials are supplied through per-request `AuthContext` values rather than retained as reusable client configuration. This satisfies API-03, API-04, and API-15.

### 9. Stateless client and explicit session ownership — PASS

`Client` has no account authorization state; account context is attached per request ([`interface.go`](../../pkg/ring/interface.go#L264)). Login, event, signaling, device, playback, and push lifecycles are explicit objects with close/error APIs ([`interface.go`](../../pkg/ring/interface.go#L100)); session owners cancel and close their work. The signaling writer test forces TCP backpressure, confirms a frame write starts, cancels the send, and asserts both the caller and writer finish ([`signaling_connection_test.go`](../../pkg/ring/signaling_connection_test.go#L56)). `Client.Listen` checks the context error before classifying a concurrently closed event socket ([`client_events.go`](../../pkg/ring/client_events.go#L88)); `TestListenReturnsContextErrorWhenCancelledWhileWaiting` cancels after upgrade and requires `context.Canceled` ([`client_event_recording_regression_test.go`](../../tests/replay/client_event_recording_regression_test.go#L19)). Replay peers wait for client socket teardown before returning, including playback and push teardown ([`session_identity_test.go`](../../tests/replay/session_identity_test.go#L92), [`playback_peer_replay_test.go`](../../tests/replay/playback_peer_replay_test.go#L60), [`session_portable_replay_test.go`](../../tests/replay/session_portable_replay_test.go#L251)). All six exact-SHA race-matrix jobs passed, including Ubuntu Go 1.24.x and 1.26.x. This closes the cancellation and replay teardown findings and supports GO-08–10 and API-09/10/13.

### 10. Injection at network boundaries — PASS

The public client accepts an HTTP client, WebSocket dialer, FCM HTTP `RoundTripper`, and connection-producing `WithFCMDialContext` hook ([`client_options.go`](../../pkg/ring/client_options.go#L44), [`client_push.go`](../../pkg/ring/client_push.go#L140)). Tests exercise the FCM dial hook and actual MCS frames over a locally terminated TLS connection ([`mcs_dial_test.go`](../../pkg/dependencies/push/mcs_dial_test.go#L38)); the public encrypted-event replay uses the same hook. The application owns Pion peer creation and media networking, as documented in [`library-architecture.md`](library-architecture.md#L17), while the library's signaling WebSocket uses the injected dialer. No uninjectable library-owned network edge was found. This satisfies LIB-18.

### 11. Explicit token exchange and refresh — PASS

[`client_auth.go`](../../pkg/ring/client_auth.go#L11) exposes authentication and refresh operations that return the current `AuthResponse`; the reusable client does not retain the returned account tokens. The authentication guide explains caller-side storage and renewal, and the token-exchange example saves the returned values. This satisfies API-03 and the explicit token ownership requirements.

### 12. Customer guides as rendered MDX — PASS

All customer guides are `.mdx` files under `docs/guides/`, and [`meta.json`](../guides/meta.json#L1) supplies site navigation. The shared Fumadocs action is pinned at v0.3.1 in [`api-docs.yml`](../../.github/workflows/api-docs.yml#L18); the exact-SHA CI docs-site job rendered guides with OpenAPI and AsyncAPI references and checked internal links ([`ci.yml`](../../.github/workflows/ci.yml#L105)). The implementation SHA changes no guide or API schema. The most recent API Documentation publication succeeded for the preceding commit with unchanged site sources, and this exact-SHA docs-site job rebuilt all rendered pages successfully. This satisfies LIB-16/17.

### 13. Rendered copy and destinations — PASS

The customer pages state their purpose and evidence status, and the release note points to published guide paths while limiting claims to behavior demonstrated by fixtures ([`v0.5.0.md`](../releases/v0.5.0.md#L1)). The exact-SHA rendered-site internal-link check passed in CI. I also checked the external guide and release-note destinations present in the reviewed MDX/release note: the five GitHub example links and four published guide links returned HTTP 200. The GitHub Pages publication succeeded for the preceding commit; this implementation SHA changes no customer-facing site source, and the exact-SHA docs-site build passed. No stale destination or copy discrepancy was found in the reviewed pages.

### 14. Independent report and final-commit disposition — PASS

This independent report evaluates every checklist item and linked standard against implementation commit `916f23132b27b720e0aef95fa26eb5c63b52c809`. Findings F-1 through F-5 are resolved, all implementation and CI items pass, and this report is linked from [`docs/template-checklist.md`](../template-checklist.md#L5). The implementation commit's full race matrix and required gates passed before these documentation-only sign-off updates, as required by GO-15 and checklist item 14.

### 15. Paired request/response and bidirectional replay — PASS

The HTTP replay transport matches method, origin, escaped path, query, relevant headers, and request body before returning the paired response; it rejects mismatches and exposes `AssertConsumed` ([`http.go`](../../internal/testkit/replay/http.go#L137), [`replay_test.go`](../../internal/testkit/replay/replay_test.go#L96)). Fixture docs distinguish captured/historical/synthetic data. The MCS offline replay fully compares the protobuf LoginRequest (including rejection cases for omitted settings and unknown fields), checks both heartbeat directions, and replays an encrypted `DataMessageStanza` into the public FCM event API ([`mcs_dial_test.go`](../../pkg/dependencies/push/mcs_dial_test.go#L38), [`fcm_mcs_ring_event_replay_test.go`](../../tests/replay/fcm_mcs_ring_event_replay_test.go#L33)). Failure-session tests now strictly decode generated ping and close frames, reject unknown fields and trailing JSON, and assert exact method, dialog, RIID, and body identity ([`session_failures_test.go`](../../tests/replay/session_failures_test.go#L314)). The CLI replay waits for ordered output milestones and server assertions before advancing its input transcript ([`diagnostic_cli_view_test.go`](../../tests/replay/diagnostic_cli_view_test.go#L135)). The paired synthetic HTTP, signaling, and MCS tests passed exact-SHA CI. This satisfies LIB-04/05.

## Finding and disposition

### F-1. Canceled signaling write test was timing-sensitive — RESOLVED

The prior commit `df5f85d05c0703e5e37f09a078886cbf82b4d27d` failed `TestSignalingWriteDeadlineInterruptsBlockedSocketWrite` while the peer used a fixed sleep and an 8 MiB payload. An earlier change in the reviewed commit's ancestry replaced that setup with a peer read gate, a 4 KiB client send buffer, a write-start observer, and cancellation assertions at [`signaling_connection_test.go`](../../pkg/ring/signaling_connection_test.go#L24). The replacement test passed ten local race-enabled repetitions; the full exact-SHA race matrix is green in CI run [36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097).

**Disposition:** Resolved. The replacement writer test is in the reviewed ancestry and the final exact-SHA matrix passes. F-2 is tracked and resolved separately below.

### F-2. Replay peer teardown raced with client signaling shutdown — RESOLVED ON THIS SHA

The prior exact-SHA run [36634341425](https://github.com/portpowered/go-ring/actions/runs/36634341425) failed `go test -race ./...` on Ubuntu Go 1.24.x and 1.26.x in replay teardown scenarios, reporting `connection closed: signaling send failed`. Replay peers now consume expected close/unsubscribe frames and wait for client socket shutdown ([`session_identity_test.go`](../../tests/replay/session_identity_test.go#L92), [`session_ordering_replay_test.go`](../../tests/replay/session_ordering_replay_test.go#L73), [`signaling_extras_public_replay_test.go`](../../tests/replay/signaling_extras_public_replay_test.go#L110)); playback and remote-close scenarios use explicit completion channels ([`playback_peer_replay_test.go`](../../tests/replay/playback_peer_replay_test.go#L60), [`session_identity_test.go`](../../tests/replay/session_identity_test.go#L267)). The corrected suite passes exact-SHA CI run [36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097), including all six OS/Go race-matrix jobs.

**Disposition:** Resolved on implementation commit `916f23132b27b720e0aef95fa26eb5c63b52c809`. The full 13-job exact-SHA run is green, including all six race-matrix entries, coverage, lint, schema-generation, fixture-contracts, public-consumer, docs-site, and API-compatibility.

### F-3. Event-listener cancellation could surface a socket-close error — RESOLVED

When the caller canceled `Client.Listen` while `Receive` was blocked, the event socket's cancellation goroutine could close the socket first; the listener then returned a transport close error instead of the caller's context error. This behavior failed the coverage job in CI run [36637358132](https://github.com/portpowered/go-ring/actions/runs/36637358132). `Client.Listen` now checks `ctx.Err()` before classifying the receive error ([`client_events.go`](../../pkg/ring/client_events.go#L88)). The regression test cancels only after WebSocket upgrade and requires `errors.Is(err, context.Canceled)` ([`client_event_recording_regression_test.go`](../../tests/replay/client_event_recording_regression_test.go#L19)). The exact-SHA coverage and all race-matrix jobs passed.

**Disposition:** Resolved in the reviewed implementation ancestry and verified at `916f23132b27b720e0aef95fa26eb5c63b52c809` by CI run [36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097).

### F-4. Failure replay did not verify the complete signaling frame — RESOLVED

Earlier expiry and heartbeat peers checked only selected fields or searched serialized payloads for a method. That allowed an extra, missing, or malformed wire field to go unnoticed. The current peer decodes into generated `SessionPingFrame` and `SessionCloseFrame`, rejects unknown keys and extra JSON values, and asserts the method, dialog, negotiated RIID, and exact session body identity ([`session_failures_test.go`](../../tests/replay/session_failures_test.go#L314), [`session_identity_test.go`](../../tests/replay/session_identity_test.go#L150)).

**Disposition:** Resolved in implementation commit `916f23132b27b720e0aef95fa26eb5c63b52c809`; the exact-SHA fixture-contract and race-matrix jobs passed in CI run [36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097).

### F-5. Diagnostic CLI replay advanced inputs before media output — RESOLVED

The earlier CLI replay wrote all arrow keys after a fixed one-second delay. A local replay run could reach session activation but fail to emit `First video packet received` before the assertion, leaving output timing coupled to machine speed. The current test waits for remote SDP, active state, first video packet, and each directional acknowledgement before sending the next input; its synchronized output writer avoids missed notifications ([`diagnostic_cli_view_test.go`](../../tests/replay/diagnostic_cli_view_test.go#L135), [`diagnostic_cli_view_test.go`](../../tests/replay/diagnostic_cli_view_test.go#L189)).

**Disposition:** Resolved in implementation commit `916f23132b27b720e0aef95fa26eb5c63b52c809`. The focused race run passed twice locally, and the exact-SHA coverage and all OS/Go matrix jobs passed in CI run [36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097).

## Check evidence

- Focused local race tests passed twice: `go test -race ./tests/replay -run '^(TestDiagnosticCLIViewAndArrowReplay|TestNegotiatedHeartbeatRejectsInvalidPresentValues|TestNegotiationCancellationAndPendingRPCFailure)$' -count=2 -timeout 180s`.
- Exact-SHA CI coverage: replay 3,050/3,579 (85.22%); unit 2,476/3,579 (69.18%); combined 3,340/3,579 (93.32%); configured package floors passed. Combined package results are WebSocket 90.8%, REST 95.0%, push 88.7%, and `pkg/ring` 92.8%.
- Exact-SHA CI run [36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097): all 13 jobs passed—lint, coverage, schema-generation, fixture-contracts, public-consumer, docs-site, API compatibility, and all six OS/Go race jobs.
- Unrelated untracked local artifacts `.codex-replay-lint-sandbox/`, `cmd/go-ring/go-ring.exe`, `coverage`, and `routegate-lint.json` were preserved.
