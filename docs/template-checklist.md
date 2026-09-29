# Go-ring template checklist

Copied from go-third-party-template. Check each item only after its evidence and independent final-commit review pass.

Independent v0.5.0 review: [review report](developer-facing/independent-review-v0.5.0.md), covering implementation commit `1d4974750aad2d5c07a4b934a230fa1c88505423` and exact-SHA CI run [36635837736](https://github.com/portpowered/go-ring/actions/runs/36635837736) (all 13 jobs passed).

- [x] **1.** Keep the public client, examples, README, and site independent of any consuming application. Put application adapters and rollout plans in the consuming repository.
- [x] **2.** Document supported operations, authentication, errors, and transport injection with examples that match the exported API. Add customer-facing operation guides for important workflows, and distinguish verified behavior from synthetic examples and historical references.
- [x] **3.** Show Go version, CI, coverage, release, Go Reference, license, and documentation badges in the README. Replace every example repository value and point badges to live reports.
- [x] **4.** Generate the API reference in CI with the shared Fumadocs action and publish it to GitHub Pages.
   Inventory **ALL** outbound wire endpoints and exchanges, including private, encrypted, event, and
   signaling routes. Every endpoint must be represented in a checked-in protocol schema. Generate
   **ALL** endpoint definitions, method/path pairs, parameter and header names, channel names, and
   wire request/response types from those schemas. Include nested event properties and payloads
   serialized inside strings or encrypted wrappers; use generated artifacts at every wire boundary,
   and do not sign off while a handwritten wire definition or model remains.
   Include active network calls made by pinned third-party dependencies, even when the library only
   supplies a transport wrapper. Keep their contracts in separate checked-in external schemas and
   source-matched protocol files; inventory the exact dependency version, host, method, path, framed
   message, and call site. A narrow source-gate exception may identify a verified wrapper, but it
   must reject unlisted dependency traffic and must not bypass schema or paired replay checks.
   Audit non-HTTP sockets separately from HTTP RoundTrippers. Fail CI on generation
   drift or an uncovered method-and-path pair, channel, or call site. The source gate must pair each
   generated operation or channel with the target at the actual wire call site after evaluating
   supported route transformations; checking only a constructor or raw literals is insufficient.
   Resolve route assignments by lexical binding and control flow; trust only values proven on every
   path to the send, not a generated assignment made in just one conditional branch. Test negative
   cases for unschematized endpoints and channels, mismatched methods and paths, and targets changed
   after schema-bound construction, including appended or wrapped paths and invalid formatting. A
   generated path does not approve an arbitrary authority in a formatted full URL; accept only an
   explicitly configured or inventoried authority, and require REST base prefixes to come from
   configured or inventoried origins. Exercise lexical shadowing so a same-named local variable
   cannot inherit another scope's generated route. Require generated `QueryParam` and `Header` keys
   for query setters and direct or aliased map writes, map literals, request headers, and
   custom-header maps. Normalize parenthesized map receivers and indexed expressions before checking
   query/header keys, and reject aliases to query or header `Set`/`Add` method values that could
   bypass key validation. Preserve request-header provenance through `http.Header(req.Header)`
   conversions and aliases. Reject request Header map escapes except to a named, inventoried helper
   whose header writes are schema-checked. Resolve custom-header maps by lexical binding; a nested
   same-named map with generated keys must not hide an outer raw-key map that is sent. Reject
   custom-header maps returned from or passed through unverified helpers. Recursively inspect
   aggregate helper arguments for embedded schema-keyed maps; allow only a direct handoff to a
   verified wire helper whose writes are schema-checked. Reject schema-bound `url.Values` maps and
   their aliases when they escape as arguments or receivers to unverified helpers that could add
   handwritten query keys. Track `url.Values` provenance by lexical binding and assignment; discard
   generated-map trust after reassignment from an untrusted source, and never accept `URL.Query` or
   `url.ParseQuery` results as outbound generated-key sources. Reject storing schema-keyed query or
   header maps in aggregate fields or indexed elements, even without a helper call; later field or
   index writes must not regain trust. Retain query/header map provenance only through aliases
   proven local to the current function; reject storing those maps in package-level or other
   cross-function state. Reject unresolved address-taking or pointer/helper escapes for route
   strings, query maps, and header maps that could permit mutation; accept them only when the gate
   proves the value remains safe. Resolve generated selector qualifiers such as `apiroutes`, the
   model-constant package, and `fmt` to their exact expected import paths, not just matching local
   import names; `http.NewRequestWithContext` must resolve through the exact `net/http` import.
   Treat `len(params)` as a built-in call only when `len` resolves to
   the Go builtin. An approved authority or base URL field must resolve to the actual Client
   receiver declared by the method. An inventoried `Client.Do` must use that receiver's actual
   injected client field object, not a same-spelled shadowing local. Exercise direct outbound
   network primitives, imports, method values and method expressions, including local and file-scope
   aliases such as package-level references to transport helpers and injected client methods. Cover
   request and URL aliases, mutations after construction, aggregate fields and indexed storage, and
   values escaping to helpers; each uncovered route, channel, or network edge must fail. Distinguish
   provider-verified contracts from implementation-derived contracts and never present the latter as
   official behavior. Replace the template's clearly synthetic widget schema before presenting a
   provider API as supported behavior.
- [x] **5.** Run offline build, lint, race, and replay checks before release. Configure golangci-lint v2 with the literal `linters.default: all`, pin its version in CI, and make the full-repository lint run a blocking gate. Do not set `--issues-exit-code=0`, continue after lint failures, or limit CI to new issues. Keep all linters enabled; any exception must name the narrow rule and affected path, give its reason in a config exclusion or beside a source annotation, and receive independent review. Style fixes must preserve persisted example/config JSON keys; lock them with a regression test or document an intentional key migration. A reviewer who did not implement the migration must confirm passing blocking CI on the exact commit before checking item 5. Keep real captures sanitized and separate from synthetic fixtures.
- [x] **6.** Add deterministic synthetic request, response, error, and session fixtures for supported behavior. Measure coverage of non-generated production code by public and transport package and in combination; enforce at least 80% combined coverage in CI and target 90%. Report generated-code exclusions and remaining uncovered behavior rather than adding tests solely to raise a number.
- [x] **7.** Put the reusable public provider package under `pkg/<provider>` and its private wire types under an appropriate `internal` package. Keep examples, generated models, and transport packages in clear, separate locations. Verify public import paths from a separate consumer module.
- [x] **8.** Initialize clients through explicit functional options (for example `NewClient(WithBaseURL(...), WithHTTPClient(...))`) with sensible defaults and validation. Keep account credentials out of reusable client configuration when the client serves multiple accounts.
- [x] **9.** Keep the reusable client stateless with respect to accounts and connections. Return explicit session objects for login, event streams, sockets, RTC, or other stateful lifecycles; make ownership, close, errors, and token state visible to callers.
- [x] **10.** Allow callers to inject the transport at every network edge the library uses, including HTTP, HTTP/2, WebSocket, MQTT, RTC signaling, and sockets opened by dependencies as applicable. A configurable concrete dialer is insufficient when it cannot substitute an offline connection; provide a connection-producing dial hook or equivalent seam and test the actual framed request and response through it without real credentials or network access.
- [x] **11.** Expose token exchange and refresh as explicit operations that return the current credentials to the caller. Do not silently refresh or retain updated tokens inside a reusable client; document caller storage and renewal responsibilities.
- [x] **12.** Publish all customer-facing guides as MDX files under `docs/guides/` in the GitHub Pages site. Link guides to the matching generated reference pages. Keep separate repository Markdown only for contributor and release process notes; check internal links from **all** rendered pages, including the site root and generated references, and review external destinations and release-note links after a docs migration.
- [x] **13.** Before release, edit every published page for concise copy: remove repeated caveats, stale claims, and links to duplicate repository documents; keep each page's purpose, evidence status, and next action clear. Review the rendered Pages site, not just the source files. Check release-note copy and URLs against the published guide locations.
- [x] **14.** Before signing off a library migration or release, have an independent reviewer who did not implement the change audit the library against every item in this checklist and the linked standards. Have that reviewer write a repository document with a separate verdict and concrete evidence for every numbered item, the reviewed commit, discrepancies, and the disposition of every finding; link it from the library's checklist. Keep this item unchecked while any finding or other checklist item remains open; recording or tracking a finding does not resolve it. Re-run affected checks and have the reviewer verify every fix at the final commit before checking this item. Do not accept an implementer's own checklist sign-off as independent verification.
- [x] **15.** Store and replay each wire exchange as a paired request and response (or an ordered bidirectional message transcript). Include method, origin, escaped path, repeated query values, relevant headers, and body or frame payload in the request expectation; include response status, relevant headers, and body. Match the outbound request before returning its response, reject unexpected or duplicate calls, and assert that every expected exchange was consumed in order where order matters. Never fall back to a response when request matching fails. Represent volatile IDs, timestamps, signatures, and redacted credentials with explicit match rules that still validate their format or decoded meaning. Apply this to every supported transport and classify each pair as captured or synthetic; a response-only fixture does not satisfy replay verification.

For this library's implementation rules, see [Go](standards/go.md),
[schemas](standards/schemas.md), [client API](standards/client-api.md), and
[library verification](standards/library.md). The independent review above
records evidence for all 15 items against the exact implementation commit and
green CI run.
