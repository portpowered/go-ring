# Independent review record

At the user's request on 2026-10-04, final dual review and checklist closure
are deferred while known customer-facing fixes, required CI, and releases
are completed. These reports audit earlier snapshots and do not approve
the final release. Unverified checklist items remain open.

Status: **open; findings and final dual approval remain outstanding**.

The two reports below independently audit all 16 checklist items at source
`cef1b87d2e3aa8fc6c2740b70f8184bfbd4ca9a5`, against shared template
`62cc3cb5a1308dae8700f92052f99b1c455a1d98`.
They were delivered before either reviewer read this record or the other report.
Later implementation changes require verification on the final commit.
See the [current checklist](template-checklist.md). Historical reports remain
in Git history. Passing CI does not resolve the findings below.

## Reviewer 1 — initial independent audit

### Independent review — initial source snapshot

**Reviewer:** Codex independent reviewer 1 (`/root/ring_blind_final_1`)

**Reviewed source:** `cef1b87d2e3aa8fc6c2740b70f8184bfbd4ca9a5` (parent-provided source manifest confirms every tracked file in the isolated sanitized checkout matches this SHA)

**Review date:** 2026-10-04 (local; GitHub run completed 2026-10-05 UTC)

**Review scope:** all 16 items in `docs/template-checklist.md` and the relevant `docs/standards/`. I did not inspect repository history, `docs/independent-review.md`, the original checkout, or another reviewer’s report. No source edits were made. Compile-valid negative probes ran only in the separate temporary probe copy.

#### Verdict summary

| # | Verdict | Class |
|---:|---|---|
| 1 | PASS | — |
| 2 | FAIL | Source blocker: canonical schemas do not contain the required validated examples. |
| 3 | PASS | — |
| 4 | FAIL | Source blocker: route gate misses mutable request-body backing storage; complete model/use inventory is not present. |
| 5 | PASS | Exact-source blocking CI, local `make check`, and pinned v2.3.0 lint pass. |
| 6 | PASS | Required replay/unit/combined thresholds pass and generated-code exclusions are reported. |
| 7 | FAIL | Source blocker: REST schema and generated model are monolithic rather than grouped by API responsibility. |
| 8 | PASS | — |
| 9 | PASS | — |
| 10 | PASS | — |
| 11 | PASS | — |
| 12 | PENDING | Publication/rendered-page evidence was not available for independent visual/content inspection. |
| 13 | PENDING | Rendered Pages were not reviewed and the current review record was intentionally withheld until this initial report. |
| 14 | PENDING | PR is open, no independent reviews are recorded, and source blockers remain. |
| 15 | FAIL | Source blocker: strict HTTP replay accepts authority/userinfo/malformed-query identity mutations. |
| 16 | PENDING | CLI public release/proxy check is unpublished; conditional browser-callback requirement needs provider-contract evidence. |

#### Item evidence

##### 1. Public-library independence — PASS

The public client is under `pkg/ring`; examples and customer material describe the reusable Ring SDK, not a consuming app or backend. I found no consumer-specific adapters or rollout instructions in the reviewed public README, examples, site sources, or API package. This matches LIB-14.

##### 2. Customer/API documentation and canonical examples — FAIL (source blocker)

The README and MDX guides cover authentication, client configuration, device operations, signaling, push and CLI usage, and identify synthetic/reference behavior. However, the checked-in canonical `api/openapi.yaml`, `api/asyncapi.yaml`, `api/client-models.openapi.yaml`, and `api/external/fcm.openapi.yaml` contain no OpenAPI `example`/`examples` nodes. The payload fixtures are kept separately under `tests/replay/fixtures`; they are not examples inside their owning canonical schemas. Thus the checklist’s sanitized, schema-valid request/response/event examples and “validate every example against its owning schema” requirement is unmet. CI schema/fixture checks passing does not supply the missing examples.

##### 3. README badges — PASS

The README presents Go version, CI, coverage, release, Go Reference, license and documentation badges linked to the project’s corresponding reports or release/site locations. `gh release list --repo portpowered/go-ring` confirmed v0.6.0 is the current latest release at review time.

##### 4. API reference, endpoint/model gate and generated wire boundaries — FAIL (source blocker)

Exact-source CI’s `docs-site` job built the generated references and reported internal links across 81 rendered pages, but the source gate has a concrete missed mutation: a generated request can retain a `bytes.Reader` over a mutable slice, then the slice can change before send, and `make routegate` still reports coverage. I reproduced this with a compile-valid file in the isolated probe copy:

```go
package rest

import (
    "bytes"
    "context"
    "github.com/portpowered/go-ring/internal/generatedhttp"
)

func (c *Client) blindReviewBodySliceMutationProbe(ctx context.Context, deviceID int64) error {
    body := []byte(`{"reason":"synthetic"}`)
    request, err := generatedhttp.NewUnlockIntercomRequestWithBody(
        generatedServerBase(c.baseURI), generatedhttp.DeviceId(deviceID), "application/json", bytes.NewReader(body),
    )
    if err != nil { return err }
    body[2] = 'x'
    return c.doGeneratedJSON(ctx, request, nil)
}
```

From the probe-copy root, both `go test ./pkg/dependencies/rest` (compile check) and the exact default `make routegate` succeeded; the latter printed `routegate: contracts and outbound callsites are covered`. The source checkout itself was not changed. The finding is against SCHEMA-19 and checklist item 4’s required mutable-body negative test.

The probe copy also executed the helper through an injected RoundTripper and inspected the emitted body. The compile-valid test at C:/Users/andre/AppData/Local/Temp/go-ring-blind-review-gb6le8kf/probe/pkg/dependencies/rest/blind_body_probe_test.go passed and confirmed the emitted body had the first field key changed from reason to xeason. Command: go test -v ./pkg/dependencies/rest -run TestBlindReviewBodySliceMutationProbeReachesRoundTripper. Root-default make routegate still passed in that same probe copy.

Separately, I parsed the checked-in YAML schemas independently of the repository’s model-inventory test. The four OpenAPI/AsyncAPI files contain **223 named schema components and at least 177 nested anonymous object schemas**; the inventory document has five responsibility rows and names only four anonymous signaling positions. It does not trace every wire type, nested object, enum/primitive semantic projection, generator output and actual conversion/use site. See the independently generated population listing at `C:\Users\andre\AppData\Local\Temp\go-ring-blind-review-gb6le8kf\wire-schema-population.md`. Since these requirements are conjunctive, the endpoint/model-generation item cannot pass on a successful docs build or existing route-gate result.

##### 5. Blocking checks and lint — PASS for this reviewed commit

I ran Go 1.24.2 `make check` and `make GOLANGCI_LINT=C:/Users/andre/go/bin/golangci-lint.exe lint` with golangci-lint v2.3.0; both passed. `.golangci.yml` sets the literal `linters.default: all` and CI pins v2.3.0. GitHub Actions run [37251628643](https://github.com/portpowered/go-ring/actions/runs/37251628643) has head SHA `cef1b87d2e3aa8fc6c2740b70f8184bfbd4ca9a5`, completed successfully, and all 13 jobs are successful: six OS/Go matrix jobs, `api-compatibility`, `coverage`, `docs-site`, `fixture-contracts`, `lint`, `public-consumer`, and `schema-generation`. I am independent of implementation and verified the exact-SHA CI result. This pass does not resolve the source findings under items 2, 4, 7, 15 or 16.

##### 6. Synthetic replay and coverage — PASS

Exact-SHA coverage CI reports replay **85.06%**, unit **71.35%**, and combined **93.65%** on non-generated production code, above the 80% combined minimum and the stated 90% target. The coverage tooling reports generated-code exclusions and remaining uncovered production behavior. Replay fixture material is classified separately as captured, synthetic, baseline or historical/reference in contributor documentation and fixture metadata. This satisfies the measured threshold and reporting portions of checklist item 6/LIB-07.

##### 7. Package/schema responsibility boundaries — FAIL (source blocker)

`api/openapi.yaml` is one 114,834-byte schema containing 39 operations and 77 components across authentication, recordings, devices, controls, timelines, locations and feature payloads. The REST provider model output is one 922,064-byte `pkg/dependencymodels/rest/models.gen.go` file with 255 struct declarations, generated from that single schema by the `generate-api` command. This is precisely the monolithic REST schema/model grouping the checklist and SCHEMA-17 prohibit. Public semantic projections are separate, and I found no second generic `internal/models`/`internal/wire` bucket, but those positives do not satisfy the required separate responsibility schemas and generated files. The five-row inventory also is not the required complete per-model mapping.

##### 8. Client options/defaults/validation — PASS

`NewClient` uses explicit functional options for endpoints and injected transports, has defaults and validates invalid endpoints/transports. Credentials are not part of reusable client configuration. The public configuration guide describes the same options.

##### 9. Stateless reusable client and account isolation — PASS

Login, event, signaling and push state are represented by explicit lifecycle objects. Login owns its own HTTP client/cookies, and the shared injected `http.Client` is rejected if it has a cookie jar; token refresh returns credentials to the caller. Existing tests exercise two accounts through one client and assert independent authentication requests/cookie state. This meets the added injected-state/cookie requirement.

##### 10. Offline injection at network edges — PASS

HTTP client, WebSocket dialer, FCM HTTP transport and a connection-producing FCM/MCS dial hook are injectable. The MCS hook is exercised with an offline TLS `net.Conn` test that exchanges frames; WebSocket tests use local paired servers/fakes. The SDK leaves the WebRTC peer to the caller, and the CLI’s synthetic paired-server tests exercise its RTC flow without real credentials or remote network.

##### 11. Explicit token exchange/refresh — PASS

Login and refresh return token values to callers; the reusable client does not retain them or perform silent refresh. The authentication guide explains caller-owned storage and renewal responsibilities. CLI token refresh and export are explicit commands.

##### 12. Published customer guides and rendered references — PENDING (publication/evidence)

Customer guides are MDX under `docs/guides/`, link to generated references, and the exact-SHA docs job built the site and passed the internal-link scan across 81 rendered pages. The run exposed no downloadable artifacts (`gh api repos/portpowered/go-ring/actions/runs/37251628643/artifacts` returned zero), and the PR site was not published for inspection. Therefore I could not inspect the actual rendered fields, examples, generated snippets, external destinations or guide rendering in the delivered Pages site. `externalDocs` is absent from the checked-in schemas, so no hidden schema-supplied links were found. Keep this pending until a rendered artifact or published Pages build is inspectable and those checks are completed; do not infer content correctness from build/link success.

##### 13. Documentation audience and rendered-site review — PENDING (evidence)

I reviewed the tracked README, contributor/release material, schema guide, fixture/provenance material, API README, customer MDX guides and other tracked docs, excluding only the current `docs/independent-review.md` because the assignment explicitly withholds it until this initial report. Repository docs generally separate customer material from contributor evidence and keep README scope focused. The required rendered Pages review could not be completed for the same artifact reason as item 12; the excluded current record also remains unreviewed. This is an evidence hold, not a sign-off.

##### 14. Independent final review — PENDING

PR #19 is open at the reviewed head, with `reviewDecision` empty and no submitted reviews. At the time of this initial report, this was the first of two required independent reviews and had not yet been added to the repository review record; the second review and re-verification of fixes at the final implementation SHA were still pending. Source findings remain open. `docs/template-checklist.md` explicitly requires two independent final-SHA reviews and says this item must stay unchecked while any other item is open.

##### 15. Paired HTTP replay identity — FAIL (source blocker)

`internal/testkit/replay/http.go:249-257` compares method, `URL.Scheme + "://" + URL.Host`, escaped path, `request.URL.Query()` pairs, headers and body, but not `Request.Host`, `URL.User`, or validity of the original `RawQuery`. `URL.Query()` silently ignores malformed query pairs. I added this compile-valid test **only in the probe copy**:

```go
package replay

import (
    "encoding/json"
    "net/http"
    "net/url"
    "testing"
)

func TestBlindReviewRequestIdentityProbes(t *testing.T) {
    mutations := []struct {
        name string
        apply func(*http.Request)
    }{
        {name: "Request.Host override", apply: func(req *http.Request) { req.Host = "attacker.example" }},
        {name: "URL user information", apply: func(req *http.Request) { req.URL.User = url.UserPassword("review", "synthetic") }},
        {name: "malformed raw query", apply: func(req *http.Request) { req.URL.RawQuery = "x=%zz" }},
    }

    for _, mutation := range mutations {
        t.Run(mutation.name, func(t *testing.T) {
            transport := NewTransport(Exchange{
                Request: Request{Method: http.MethodGet, Origin: "https://api.example.test", Path: "/resource", Headers: http.Header{}, Body: json.RawMessage("null")},
                Response: Response{Status: http.StatusNoContent, Headers: http.Header{}, Body: json.RawMessage("null")},
            })
            req, err := http.NewRequest(http.MethodGet, "https://api.example.test/resource", nil)
            if err != nil { t.Fatal(err) }
            mutation.apply(req)
            if _, err = transport.RoundTrip(req); err != nil { t.Fatalf("identity mutation was rejected: %v", err) }
            if err = transport.AssertConsumed(); err != nil { t.Fatalf("accepted exchange not consumed: %v", err) }
            t.Log("STRICT REPLAY ACCEPTED an altered request identity")
        })
    }
}
```

For each mutation, a strict transport with an expected `GET https://api.example.test/resource` returned a response and consumed the pair. `go test -v ./internal/testkit/replay -run '^TestBlindReviewRequestIdentityProbes$'` compiled and passed while logging `STRICT REPLAY ACCEPTED an altered request identity` for all three. This reproduces exactly the false acceptance the checklist says the negative control must reject. The probe source and full output are in `C:\Users\andre\AppData\Local\Temp\go-ring-blind-review-gb6le8kf\probe\internal\testkit\replay\blind_identity_probe_test.go`.

##### 16. Standalone CLI — PENDING (release and provider-contract evidence)

A separate cmd/go-ring module exists, consumes the public pkg/ring API, and has offline paired-transport tests for authentication failures and session/RTC cleanup. Commands cover login, explicit refresh/export/logout, discovery, reads/controls, events and live video. The CLI guide has an ordered command block, but its post-release install line uses the placeholder @vX.Y.Z; it cannot yet be verified as a customer-copyable published install. The public release list ends at SDK v0.6.0, and I could not verify a CLI v0.7.0 tag or a public-proxy consumer installation.

The public SDK login flow is PKCE based and internally follows Ring OAuth authorize redirects, but the SDK exposes only NewLoginSession(Username, Password), Request2FACode and Authenticate(OTPCode). It has no caller-owned browser, redirect URI, callback listener or authorization lifecycle. The schema advertises OAuth response_type=code, redirect_uri and state, but that implementation-derived schema is not evidence that Ring accepts caller-owned localhost redirects. Because checklist item 16 makes browser/callback support conditional on provider capability, I leave it pending rather than assert that capability. If a provider contract confirms caller-owned redirects, the current source has a source blocker; if Ring restricts the callback to its own service URI, document that with provider evidence. The currently established hold is the unpublished CLI module/release and public-proxy installation requirement.

#### Findings and verification record

- **F1 — Schema examples absent:** item 2; source blocker.
- **F2 — Mutable request body bypasses route gate:** item 4 / SCHEMA-19; source blocker; compile-valid probe plus default root `make routegate` evidence above.
- **F3 — Monolithic REST schema/model and incomplete actual-use inventory:** items 4 and 7 / SCHEMA-17, SCHEMA-19, LIB-20; source blocker. Independent schema/model population scan: `C:\Users\andre\AppData\Local\Temp\go-ring-blind-review-gb6le8kf\wire-schema-population.md`.
- **F4 — Replay accepts altered Host, userinfo and malformed RawQuery:** item 15 / LIB-05; source blocker; compile-valid test and command above.
- **F5 — CLI module publication/callback applicability:** item 16; public-proxy install is unverified before a release. Caller-owned callback support is an unresolved provider-contract question; treat it as a source blocker if provider evidence confirms the feature.
- **F6 — Rendered docs unavailable:** items 12–13; publication/evidence hold; exact-SHA site build and internal links pass but output pages were not available for inspection.
- **F7 — No final review/publication:** item 14 and CLI release portion of item 16; pending until two reviewers verify the final SHA and the CLI module is installable from the public proxy.

Local verification: Go 1.24.2 `make check` PASS; Go 1.24.2 `make GOLANGCI_LINT=C:/Users/andre/go/bin/golangci-lint.exe lint` PASS (golangci-lint v2.3.0). Probe-copy verification: `go test -v ./internal/testkit/replay -run '^TestBlindReviewRequestIdentityProbes$'` PASS with all three identity mutations accepted; `go test ./pkg/dependencies/rest` PASS (compile); root-default `make routegate` PASS despite mutable-body negative.

`/s/ Codex independent reviewer 1 (GPT-6)`


## Reviewer 2 — initial independent audit

### Independent Ring checklist audit — initial review

**Reviewer:** Independent reviewer 2 (`/root/ring_blind_final_2`)

**Date:** 2026-10-04 (America/Los_Angeles)

**Verdict:** **NOT APPROVED** — items 4 and 15 have demonstrated source gaps; item 13 has a documentation finding; item 16 has open workflow/test gaps; items 12, 14, and publication evidence remain pending.

#### Reviewed snapshot and method

- Reviewed source: original source SHA `cef1b87d2e3aa8fc6c2740b70f8184bfbd4ca9a5`, from the isolated checkout at `C:/Users/andre/AppData/Local/Temp/go-ring-blind-review-a0ntbtrm/checkout`. Its local wrapper commit is `f6be3435bd7452ec026677c0031c5c91a510c035`; the parent supplied the external archive manifest proving the snapshot matches cef1 except for a neutral review-record file. I did not inspect the original checkout, Git history/remotes, the neutral review record, or another reviewer’s report.
- Checklist/template basis: `docs/template-checklist.md` against shared template commit `62cc3cb5a1308dae8700f92052f99b1c455a1d98`; read `AGENTS.md` and all four `docs/standards/*.md` files.
- Environment: Go 1.24.2, Windows amd64; pinned golangci-lint v2.3.0. Commands used a reviewer-owned temporary `GOCACHE` and `GOLANGCI_LINT_CACHE`.
- Local checks: `make lint` passed (0 issues; golangci-lint emitted only the existing `wsl` deprecation/configuration warnings). `make check` passed, including build, schema/fixture contracts, full root tests, CLI race tests, vets, and default-root `routegate`.
- Exact-source CI: [run 37251628643](https://github.com/portpowered/go-ring/actions/runs/37251628643) has head SHA `cef1b87d2e3aa8fc6c2740b70f8184bfbd4ca9a5`; all 13 jobs succeeded, including the six OS/Go matrix checks, CLI checks, compatibility, public consumer, docs-site, coverage, schema generation, lint, and fixture contracts.
- Temporary negative probes were added only inside the isolated checkout and removed after each run. `make check` created an untracked `cmd/go-ring/go-ring.exe`; there are no tracked changes or remaining probes.

#### Findings

##### R2-1 — Item 4: the model inventory/gate does not cover all production wire structs

`docs/wire-model-inventory.md` is organized by broad responsibility, not by each production wire type and exact uses. `tools/protocols/wire_model_inventory_test.go:29` checks named schema components; its transport-file assertion at line 137 checks that selected files exist. The handwritten-struct gate is limited to signaling transport files. These checks do not establish a complete census across production packages or capture unreferenced exports and anonymous objects.

I added this compile-valid temporary production declaration to `pkg/dependencies/rest`:

```go
type BlindProbeWire struct {
    Known string `json:"known"`
    Nested struct {
        Key string `json:"key"`
    } `json:"nested"`
}
```

`go test ./pkg/dependencies/rest ./tools/protocols` passed, and the exact default-root command `make routegate` passed with `routegate: contracts and outbound callsites are covered`. This is the checklist’s expressly required negative control (unreferenced exported JSON model plus anonymous nested object) and falsifies the current complete-model-gate claim. The inventory also lacks, for every anonymous object, its exact schema/JSON/Go paths and active conversion use.

##### R2-2 — Item 13: `api/README.md` gives contributors the wrong generated-model locations

`api/README.md:3-11` says OpenAPI, AsyncAPI, and the external FCM schema generate their request/response wire models in `internal/generatedhttp`, `internal/generatedsignaling`, and `internal/generatedfcm`. The actual model generator output is under `pkg/dependencymodels/{rest,signaling,fcm}`; for example, `pkg/dependencymodels/rest/config.yaml` has `models: true` and outputs `pkg/dependencymodels/rest/models.gen.go`, while `internal/generatedhttp/config.yaml` has `models: false` and generates the client. The internal generated signaling/FCM and public compatibility files are aliases to the `pkg/dependencymodels` definitions. This contributor note therefore misstates where schema-owned wire declarations live and should be reconciled with the current model inventory.

##### R2-3 — Item 15: replay accepts an unrecorded effective HTTP authority

`internal/testkit/replay/http.go:252-253` validates `URL.Scheme + "://" + URL.Host` and escaped path, but does not validate `Request.Host` or reject `URL.User`. Two isolated compile-valid probes each showed an expected `https://example.test/` pair returning its canned response for (a) `request.Host = "evil.test"` and (b) request URL `https://user:password-secret@example.test/`. Commands were `go test ./internal/testkit/replay -run TestBlindProbeReplayHostOverride -v` and `... -run TestBlindProbeReplayURLUserInfo -v`; both probes passed their acceptance assertions and were removed. This violates the explicit effective-authority and URL-userinfo replay requirements.

##### R2-4 — Item 15: replay mismatch diagnostics include URL credentials

`internal/testkit/replay/http.go:218` stores `request.URL.String()` in the mismatch error (rendered by `noMatchingExchangeError.Error`). A temporary test submitted `https://user:password-secret@example.test/?access_token=query-secret` to an empty replay transport. It produced `replay: no unused exchange matches GET https://user:password-secret@example.test/?access_token=query-secret` (`go test ./internal/testkit/replay -run TestBlindProbeSecretDiagnostic -v`, pass); the test was removed. Redact URL userinfo and sensitive query values before diagnostics.

##### R2-5 — Item 16: CLI failure/cancellation workflow and customer sequence are incomplete

The successful CLI path is substantial: `tests/replay/diagnostic_cli_test.go:14` runs a local paired OAuth/PKCE/2FA sequence, checks saved rotated tokens, and exercises device listing, siren controls, status, and logout. `diagnostic_cli_view_test.go:19` also checks view-session teardown. I found no CLI replay covering failed authorization or `events watch` cancellation/cleanup; the CLI package tests cover refresh/status/export/logout and rendering/RTC helpers, while authorization-failure tests are in the REST SDK rather than the CLI process/workflow. `cmd/go-ring/events.go` defers connection close, but no CLI replay proves that path under cancellation. The guide’s “Common commands” block is an unordered list of commands rather than the required short copyable install → login → discovery → select a returned ID → device operation sequence; the examples use unexplained numeric IDs.

Provider-dependent authorization remains **unresolved**, not asserted as a provider fact: `api/openapi.yaml` models an authorization-code/PKCE grant and a `redirect_uri`, but public `LoginSessionRequest` accepts username/password/hardware ID only, and the public login session exposes only `Request2FACode` and `Authenticate(OTPCode)`. The REST client owns PKCE state and follows the Ring callback internally. There is no caller-owned callback/listener or browser injection API. If Ring supports customer browser consent with a caller-registered redirect, that required flow is absent; if it does not, the provider limitation and evidence need to be explicit before item 16 can pass.

The SDK v0.7 and CLI v0.7 publication/install checks are **publication-only pending** per the parent’s release-status note; no public proxy install/tag verification is claimed here.

#### Item-by-item assessment

“Meets source evidence” below is not a checklist sign-off for the release. The checklist itself remains open pending both independent reviewers and a final implementation commit.

1. **Meets source evidence.** README, examples, and guides are Ring-client focused; I found no consuming-application rollout or adapter content in customer material.
2. **Meets source evidence with item 16 open.** Authentication, configuration/injection, device operations, events, playback, recording, and lifecycle guides are present as MDX and generally use the exported request/session types. `make check` builds examples and checks protocol/fixture contracts; exact-source CI fixture contracts succeeded.
3. **Meets source evidence.** README has Go version, CI, coverage, release, Go Reference, license, and documentation badges, using the actual `portpowered/go-ring` repository rather than placeholder repository values.
4. **NOT APPROVED — R2-1.** Endpoint, generation, routegate, CI docs build/publish configuration are present and exact-source CI passed, but the demonstrated model census/gate failure is a release blocker.
5. **Meets source evidence on cef1.** `.golangci.yml` uses literal `linters.default: all`; CI pins v2.3.0 and blocks on `make lint`. Local lint/check passed and exact-source CI passed race/build/module checks. Release-tag checks remain tied to the not-yet-published release.
6. **Meets source evidence.** Coverage code enforces 85% replay and 90% combined floors plus package baselines; the exact-source coverage job passed. Generated-code exclusions are in the coverage tool.
7. **Meets source layout; public release verification pending.** `pkg/ring`, `pkg/dependencymodels/*`, and `pkg/dependencies/*` use the required boundaries, and exact-source `public-consumer` CI passed against the checkout. Public proxy verification awaits the planned SDK release. Inventory completeness overlaps the open item 4 finding.
8. **Meets source evidence.** `NewClient(opts ...Option)` has defaults and validates configuration; account credentials are request/session-scoped, not reusable client options.
9. **Meets source evidence.** Login, push, events, signaling, device-session, and playback lifecycles have explicit objects/close methods; account credentials are scoped per request. Cookie-jar sharing is rejected and account-scope replay tests exercise separate credentials on one reusable client.
10. **Meets source evidence.** HTTP, WebSocket, FCM HTTP, and MCS connection-producing seams are exposed. `pkg/dependencies/push/mcs_dial_test.go:39` uses an offline TLS peer over `net.Pipe` to match the MCS login frame, return the paired response, exchange heartbeat frames, and prove cancellation closes the connection. RTC peer creation is caller-owned by design.
11. **Meets source evidence.** `Authenticate`, `LoginSession.Authenticate`, and `RefreshToken` return token responses; the shared client does not bind refreshed credentials. Guides instruct callers to persist rotated tokens.
12. **PENDING exact rendered-content inspection.** Customer guides are under `docs/guides/`; API docs CI builds Fumadocs and checks rendered-site internal links, and exact-source `docs-site` passed. `api` schemas contain no `externalDocs` URLs to inspect. The currently deployed diagnostic CLI page is not cef1 content: a live fetch lacked the source guide’s “Until its first CLI release” and `vX.Y.Z` text, so it cannot stand in for rendered cef1 output. The CI job leaves no rendered-site artifact in this checkout; an exact rendered page review remains necessary.
13. **NOT APPROVED — R2-2; inventory limitation R2-1.** I reviewed README, contributor/release/security documents, all guide MDX, examples/CLI/third-party/replay READMEs, checklist, API notes, and standards. The wrong generated-model mapping in `api/README.md` is a concrete stale contributor claim. Exact rendered-site review remains pending. I did not inspect `docs/independent-review.md` under the blind-review protocol; the parent will supply its deferred docs-only appendix after this report.
14. **PENDING.** This is an initial independent report on cef1, not final-commit approval. Two independent findings remain open; the final repository record and both reviewers’ re-verification are outstanding. `docs/independent-review.md` was intentionally not read.
15. **NOT APPROVED — R2-3 and R2-4.** Paired HTTP/MCS/WebSocket fixtures, strict request matching, consumption assertions, and volatile OAuth/PKCE rules are present and tested. The effective-authority and secret-diagnostic probes above show two explicit required controls are missing.
16. **NOT APPROVED / PARTIAL — R2-5.** Standalone nested CLI module, routine commands, secure local token storage, explicit refresh/export/logout, paired happy-path login, and RTC cleanup exist. CLI-level failed-auth and events cancellation/cleanup workflows are not replayed; the guide lacks the required customer sequence; interactive callback support is not established; published SDK/CLI install checks are pending release.

#### Signature and disposition

Signed: **Independent reviewer 2 — `/root/ring_blind_final_2`**.

Disposition: **NOT APPROVED**. No finding above was changed or resolved during this review. Require a fresh snapshot, rerun affected negative probes/checks, inspect the exact rendered pages, and obtain both independent reviewers’ final-commit verification before closing the open items.
