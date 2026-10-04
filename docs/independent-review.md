# Independent documentation review

Current status: the expanded sixteen-item checklist is open. The scoped
documentation verdict below predates the complete-model and standalone-CLI
requirements. Two independent reviews of the final implementation are pending.

CLI work adds explicit refresh/export, JSON discovery/status, successful help,
signal cancellation, a public SDK dependency, and nested-module release checks.
Its synthetic refresh test matches the request before returning its response.
The older diagnostic CLI HTTP server checks selected paths and credentials;
its authentication and read/control cases still need complete paired request
expectations before item 16 can close. The first public CLI tag and clean proxy
installation also remain pending.

**Scoped verdict:** PASS for checklist items 2, 3, 12, and 13. Checklist item 14 remains open.

**Reviewer:** `independent_gap_audit`; not an implementation author.

**Documentation change reviewed:** `58d914a44cb8bc4f5df23dfb290615c0279d0cbe` (`docs: keep Ring guidance focused on library consumers`), merged into `ab20a9680ebe876f22f06ab161f7b60929402b1b`. The merged tree contains the reviewed documentation changes.

**Exact verification:** [CI run 37092747201](https://github.com/portpowered/go-ring/actions/runs/37092747201) passed all 13 jobs, including the rendered `docs-site` build and internal-link check. [API Documentation run 37092747691](https://github.com/portpowered/go-ring/actions/runs/37092747691) passed both build and GitHub Pages deployment. The exact published guide index, four edited guide pages, and five linked OpenAPI/AsyncAPI destinations returned HTTP 200. The repository's local checks also passed: `make lint`, `make check`, and the 29-link documentation audit.

## Root cause

The previous v0.5.0 report reviewed implementation commit `916f23132b27b720e0aef95fa26eb5c63b52c809` and CI run [36641559097](https://github.com/portpowered/go-ring/actions/runs/36641559097), not this documentation change. It marked every checklist item passed while the README contained a detailed operation inventory, capture/replay notes, and links to internal maintenance documents. The old LIB-10 standard explicitly encouraged listing each operation, method, and inline example in the README. The report cited the README table and relied on a successful rendered-site build and link check, which cannot assess audience or duplication in the README and other tracked Markdown excluded from the site.

This change revises LIB-10 to keep the README customer-facing and adds LIB-19 to require an audience, purpose, duplication, and incoming-link review of every tracked document, including off-site Markdown. It removes obsolete internal reports and duplicate process notes and consolidates fixture-format instructions in the fixture guide. The earlier review does not verify these corrections.

## Scoped checklist verdicts

### 2. README audience and public examples — PASS

The revised [README](../README.md) covers installation, authentication, device listing, a live-session overview, runnable examples, and links to customer guides, the generated reference, and the CLI guide. It no longer carries the operation-by-operation verification table, capture provenance, raw schema links, or internal architecture/replay references. The caller supplies account tokens and closes login/session objects; the linked live-session guide explains ownership of the WebRTC peer. I checked the README calls and request types against the public declarations in `pkg/ring/login_session.go`, `pkg/ring/client_devices.go`, `pkg/ring/client_session.go`, and `pkg/ring/device_session.go`.

### 3. README badges and repository identity — PASS

The README shows Go version, CI, replay coverage, release, Go Reference, license, and documentation badges. Repository links use `portpowered/go-ring`; the release badge points to the latest release, and the license badge links to `LICENSE`. I checked the badge and README destinations with GET requests; the coverage report, Go Reference, release page, GitHub Pages root, and guide pages returned HTTP 200.

### 12. Published customer guides and navigation — PASS

Customer guides are MDX pages under `docs/guides/`. The guides index links to each customer workflow, and the edited ticket and push guides link to generated OpenAPI or AsyncAPI operations. `CONTRIBUTING.md` and `tests/replay/fixtures/README.md` contain contributor and fixture-maintenance material outside customer navigation. The source scan found no remaining Markdown links to the deleted duplicate documents. The exact-SHA rendered-site check passed, and API Documentation run 37092747691 published the reviewed site successfully.

### 13. Published copy and destinations — PASS

The edited customer pages remove fixture-provenance and internal verification paragraphs while retaining caller choices, lifecycle instructions, and user-visible limits, including playback availability and account/device/region variability. The README links readers to customer guides rather than carrying maintenance inventories. I reviewed the rendered pages and followed the generated operation links: all five destinations linked from the changed ticket, push, and playback guides returned HTTP 200. No release note changed in this commit.

## Open scope and disposition

- Checklist item 14 remains **open**. This review covers only items 2, 3, 12, and 13; it is not a full review of all checklist items and linked standards, and it does not support a complete migration sign-off.
- Existing checkmarks for items 4 and 7 are historical and were not re-verified here. In particular, this documentation-only change does not establish the complete production wire-model inventory required by LIB-20 or generated-model grouping described by SCHEMA-17.
- No unresolved finding remains within the reviewed documentation scope.

## R1 full checklist review — c65a88a90899d9037a43c3e2e809f348a5ab4f4a

**Reviewer:** `/root/alexa_behavior_payloads`; no Ring implementation changes.

**Reviewed commit:** `c65a88a90899d9037a43c3e2e809f348a5ab4f4a` (`Validate complete CLI signaling transcripts and connection teardown`). The worktree was at this exact commit. The pre-existing untracked `cmd/go-ring/go-ring.exe` was not inspected or changed.

**Exact CI:** [run 37164559488](https://github.com/portpowered/go-ring/actions/runs/37164559488) completed successfully with all 14 jobs green, including lint, schema generation, fixture contracts, docs site, coverage, API compatibility, and public consumer.

### 1. Application independence — PASS

The reusable client and examples live in `pkg/ring` and `examples/`; the optional terminal adapter is isolated in the nested `cmd/go-ring` module. The README presents a Ring SDK and does not describe or depend on a consuming application.

### 2. Public API documentation — PASS

The README shows authentication, device discovery, and session ownership using exported `pkg/ring` APIs. Customer workflow guides are under `docs/guides/`, including authentication, configuration, device operations, push, playback, and live sessions. `api/README.md` distinguishes captured, synthetic, historical, and implementation-derived contracts; the generated reference is linked from the README and guides.

### 3. README identity and badges — PASS

The README has Go version, CI, replay coverage, release, Go Reference, license, and GitHub Pages badges, all targeting `portpowered/go-ring` resources. Its examples and guide destinations use the same repository identity.

### 4. Schemas and complete wire inventory — OPEN

OpenAPI and AsyncAPI sources generate HTTP, signaling, and external FCM operation/model code, and the API reference build passed. However, there is no complete production endpoint, model, primitive, and nested-payload inventory. `pkg/ring/client_read_models.go` still defines JSON-shaped public read models, including `DeviceDetailDevice`, `DeviceStatus`, `LocationSummary`, and `TimelineEvent`; `pkg/ring/device_session.go` still constructs known signaling payloads as maps at lines 140, 148, 339, 346, 394, 447, 459, 463, and 523. `pkg/dependencies/rest/constants.go` defines the wire value `deviceModel = "go-ring"` directly. The legacy account-event API is explicitly described in `api/README.md` as lacking a captured wire schema, while `pkg/dependencies/websocket/events.go` returns raw maps. Schema and route gates do not provide the required full model/payload/key inventory or the specified negative coverage for every production model and known nested value.

### 5. Blocking lint and exact-commit verification — PASS

`.golangci.yml` sets the literal `linters.default: all`; CI pins golangci-lint v2.3.0; the Makefile includes both the SDK and nested CLI lint targets. No global linter disable was found. The exact reviewed SHA has a successful blocking CI lint job, satisfying the independent exact-commit check for this item.

### 6. Synthetic replay and coverage — PASS

The exact coverage job reports replay coverage of 85.18% (3057/3589 maintained statements), unit coverage of 68.82%, and combined coverage of 93.42% (3353/3589), above the enforced 90% combined floor and the checklist's 80% minimum. `tools/coverage` runs replay, unit, and combined suites with per-package floors and excludes generated sources from the maintained handwritten denominator; its comments and output identify those exclusions. The exact coverage job passed.

### 7. Generated-model placement and inventory — OPEN

Generated provider models are in `internal/generatedhttp`, `internal/generatedsignaling`, and `internal/generatedfcm`; `pkg/ringapimodels/models.gen.go` contains public projections. There is no `pkg/dependencymodels` and no complete model inventory mapping each production struct to a schema component, generator, and conversion/call site. The handwritten JSON-shaped public models and known signaling maps identified under item 4 remain outside a complete generated-wire-model inventory. `tools/routegate` verifies routes and signaling adapters, but it is not the required gate against every unreferenced handwritten JSON struct, anonymous nested wire object, or compatibility export.

### 8. Client construction and validation — PASS

`pkg/ring/client.go` exposes `NewClient(opts ...Option)`. `pkg/ring/client_options.go` provides functional options for HTTP clients, endpoints, WebSocket dialing, and related configuration, with validation for endpoint values and nil transports. Defaults are applied during client construction; account credentials are not client options.

### 9. Account and session ownership — PASS

Requests carry account credentials through `AuthContext`; the reusable `Client` does not bind an account token. Login, signaling/device sessions, event connections, and push connections have explicit lifecycle types and `Close` methods. `Client.Close` owns no account/session state; individual returned resources have their own close operations.

### 10. Network transport injection — PASS

The reusable SDK accepts an injected `*http.Client`, WebSocket dialer, FCM HTTP `RoundTripper`, and context-aware FCM MCS connection function (`FCMDialContext`). Tests exercise the FCM hooks and the WebSocket/signaling paths against local synthetic peers. The Pion `PeerConnection` construction is in the separate CLI module, not the reusable `pkg/ring` client.

### 11. Explicit token operations — PASS

`Client.Authenticate` and `Client.RefreshToken` return `AuthResponse` values, and login sessions expose the two-factor and credential exchange steps. Token values are request-scoped through `AuthContext`; the authentication guide assigns secure storage and renewal to the caller. The CLI refresh action is explicit and its tests check persistence without printing token values.

### 12. Customer guide publishing — PASS

Customer guides are MDX pages under `docs/guides/`; the index links the customer workflows and generated reference destinations. The exact commit's `docs-site` job passed, including the rendered site and link checks. The CLI guide is in the customer guide tree and describes the current build/install state.

### 13. Published copy and repository documentation — PASS

The README is focused on installation, authentication, device listing, a short session example, and links to guides and examples. The API documentation labels implementation-derived and synthetic evidence and calls out unverified behavior. The earlier scoped independent documentation review audited the tracked documentation set; for this SHA I rechecked the README, guide index, new diagnostic CLI guide, API evidence notes, and the exact rendered-site result. The diagnostic guide states that the first CLI release is pending and explains credential handling, explicit refresh/export, commands, and device/session limitations. No duplicate customer-facing process guide was found in the current tracked documentation list.

### 14. Two full independent reviews and disposition — OPEN

This is one full R1 review. The earlier `independent_gap_audit` entry covers only items 2, 3, 12, and 13, not the complete checklist. A second independent full review at the final implementation SHA is not recorded here. Items 4 and 7 have unresolved inventory/model-generation findings, and item 16 remains open, so this item must stay unchecked.

### 15. Paired HTTP and signaling replay — PASS

HTTP replay pairs contain request and response fields and match method, origin, escaped path, repeated query values, headers, and body before returning a response. `AssertConsumed` rejects unused calls; mismatch, duplicate, and out-of-order negative tests are present. The CLI OAuth paired exchange validates state format, PKCE challenge/verifier binding, hardware identity continuity, form encoding, and exact expected request/response matching (`tests/replay/cli_oauth_pairs_test.go`, `cli_oauth_rules_negative_test.go`, and `cli_http_pairs_negative_test.go`). The c65a88a changes add complete synthetic WebSocket transcripts with strict handshake checks, dynamic SDP/RPC bindings, transcript completion assertions, and peer-connection closure checks for both CLI view and snapshot tests. Fixture classifications distinguish synthetic, captured, and historical records. The exact CI fixture-contract and coverage jobs passed.

### 16. Published standalone CLI — OPEN

The CLI is a separate module with auth, device, event, snapshot, and live-view commands; its guide, paired offline tests, lint/check targets, and tag-triggered public-proxy verification workflow exist. However, no nested CLI semver release tag or successful public-proxy installation run exists. On this reviewed tree, `go list -m github.com/portpowered/go-ring/cmd/go-ring@latest` resolved to pseudo-version `v0.0.0-20261003032408-650415a55d0a`, not a published CLI version tag. The guide correctly says its first CLI release is pending.

### R1 disposition

Items 4, 7, 14, and 16 remain open. The checklist must remain unchecked; this review does not certify migration completion or release readiness.
