# go-ring checklist

Requirements copied from the shared template at `c6cfd7edf23924ba43ca994dbacabbf82ea85344`.

Final independent review and checklist closure are deferred at the user's request.
Unchecked items remain open; passing CI and a release do not establish final approval.
See the [current review record](independent-review.md).

- [ ] **1.** Keep the public client, examples, README, and site independent of any consuming application. Put application adapters and rollout plans in the consuming repository.
- [ ] **2.** Document supported operations, authentication, errors, and transport injection with examples that match the exported API. Add customer-facing operation guides for important workflows, and distinguish verified behavior from synthetic examples and historical references.
   Put synthetic, schema-valid request, response and event examples in the canonical schemas,
   including complete envelopes, nested payloads, resource updates, relation changes and failures.
   Mark each sample declaration `x-example-evidence: synthetic`; for AsyncAPI, put each `examples[]` sample's evidence on its enclosing Message Object and direct payload-schema sample evidence on that schema; the closed Example Object does not take evidence. Cover every OpenAPI request,
   success-response and failure-response schema group and every AsyncAPI message with a payload
   schema; a direct root schema or media example covers a group, but a nested-property example does
   not. Validate every declared sample, local reference and group with `go run ./tools/schemaexamples`
   as a blocking part of `make check`, pull-request CI and release checks. Never publish credentials
   or private captures as examples.
- [ ] **3.** Show Go version, CI, coverage, release, Go Reference, license, and documentation badges in the README. Replace every example repository value and point badges to live reports.
- [ ] **4.** Generate the API reference in CI with the shared Fumadocs action and publish it to GitHub Pages.
   Inventory **ALL** outbound wire endpoints and exchanges, including private, encrypted, event, and
   signaling routes. Every endpoint must be represented in a checked-in protocol schema. Generate
   **ALL** endpoint definitions, method/path pairs, parameter and header names, channel names, and
   wire request/response types from those schemas. Include nested event properties and payloads
   serialized inside strings or encrypted wrappers; use generated artifacts at every wire boundary,
   and do not sign off while a handwritten wire definition or model remains.
   Correlate command or message identifiers with their named payload schemas using discriminated
   variants or an equivalent explicit binding. The generated reference must expose each known
   variant's required parameters and result shape, even when the transport uses a generic envelope.
   An unknown or free-form payload does not document known variants. Keep future-value extensions
   separate so they cannot accept malformed known payloads. Inspect actual rendered fields,
   variant examples and generated request snippets; schema validity and HTTP 200 alone do not
   establish that customers can discover the payload they must send.
   Inventory concrete payload variants and library-defined map keys, operation values, and skill
   or message identifiers as well as structs. Generate known nested payloads and their wire
   constants from schema; a generated outer envelope around a handwritten map does not satisfy
   this requirement. Keep genuinely caller-defined open fields explicit in the schema and
   distinguish them from payload shapes constructed by the library. Add a negative gate test
   for an unregistered nested payload or library-defined wire key.
   Record each primitive value and semantic projection separately, with its schema owner,
   generated declaration, and actual package-resolved uses; similarly named SDK and transport
   values must not be conflated. Bind shared known values to their original enum and reject
   value drift or a missing binding. Preserve explicitly open future values. When a GraphQL
   JSON scalar contains known members, generate those members from a bound component without
   changing the emitted selection. Generate library-owned string templates and cookie formats
   as well as JSON objects. Negative controls must reject a novel unregistered fixed value or
   key in a generated wire object, later field mutations, local aliases, and forged generated
   markers; a denylist of already-known literal values alone is insufficient.
   Recursively resolve closed-enum constraints through schema references, nested or anonymous
   structs, and arrays or slices. Reject invalid values in caller-constructed containers for
   closed fields, while retaining positive controls for genuinely caller-defined open fields.
   Follow wire values and map provenance through helper arguments and returns,
   including named results with bare returns, local aliases, and returned callbacks.
   Add negative controls for a novel fixed value returned through a named result and
   a generated map escaping through a named result to an unverified helper. Retain
   positive controls for caller-defined values; do not treat diagnostic or error
   return values as wire payloads merely because they share a helper.
   Resolve helpers across every production file in their Go package, retaining each
   file's import bindings. Add a compile-valid two-file negative where a sibling
   helper returns an unregistered fixed value or mutates a generated map, plus
   a caller-defined positive control. A per-file scan does not prove package-wide
   provenance.
   Do not classify unresolved provenance as caller-defined input. If recursion,
   a traversal limit, or an unresolved library helper prevents proving a schema-owned
   wire value or key, fail the gate with a diagnostic. Test a helper chain beyond
   the traversal limit and a recursive fixed fallback; retain proven caller-input
   positives.
   Check all intermediate keys in indexed receiver paths and aliases, including slices,
   pointer dereferences, and type assertions. Test inferred nested composites and retain
   positive cases for explicitly caller-defined open keys.
   Build a complete model inventory as well as an endpoint inventory. For every production
   struct encoded, decoded, or embedded in a wire exchange, record its schema component,
   generated Go type, generator command, and conversion call site. Include exported dependency
   structs, primitive enums and constants, unused legacy exports, anonymous objects, nested
   feature payloads, and custom decoders. A generated file marker or passing route gate is not proof that the remaining
   structs are generated. Remove unused wire definitions; generate active ones. Test the
   model gate with an unreferenced exported handwritten JSON struct and an anonymous nested wire
   object; unused compatibility exports must not escape the scan. For each anonymous object,
   record its exact schema path, JSON field path, Go field path, schema owner, generated Go type,
   generator command, and actual encode or decode uses.
   For GraphQL, trace generated models to SDL components and actual emitted operation selections.
   Validate generator-added discriminators and exact generated decoder branches against schema
   possible types. Label type-only implementations with no selected subtype fields explicitly;
   comments naming implementations do not prove the decoder or the outbound selection. Test
   missing, extra, and mismatched decoder cases, possible-type drift, and a missing discriminator.
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
   cannot inherit another scope's generated route.
   Check the actual request object through the send: reject unregistered method,
   URL origin/path/query, header, or body mutations after construction, including
   aliases, cloned requests, and helper escapes. Add a negative control that changes
   `request.URL.Path` between a generated constructor and `Do` or its equivalent.
   Include URL user information, body factories, and HTTP framing fields: they can
   change authentication or the emitted body even when the method and path stay fixed.
   Track mutable backing buffers used by request body readers through the actual send;
   test a byte-slice mutation after request construction and retain a safe immutable-body
   positive control. Scan every shipped production module, including standalone CLI and
   example modules, for outbound edges; a scan limited to the SDK package is incomplete.
   Run negative controls through the exact CI or Makefile command with its default
   working directory and root arguments; an absolute-root helper test alone is insufficient.
   Require generated `QueryParam` and `Header` keys
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
- [ ] **5.** Run offline build, lint, race, and replay checks before release. Configure golangci-lint v2 with the literal `linters.default: all`, pin its version in CI, and make the full-repository lint run a blocking gate. Do not set `--issues-exit-code=0`, continue after lint failures, or limit CI to new issues. Keep all linters enabled; any exception must name the narrow rule and affected path, give its reason in a config exclusion or beside a source annotation, and receive independent review. Style fixes must preserve persisted example/config JSON keys; lock them with a regression test or document an intentional key migration. A reviewer who did not implement the migration must confirm passing blocking CI on the exact commit before checking item 5. Keep real captures sanitized and separate from synthetic fixtures.
- [ ] **6.** Add deterministic synthetic request, response, error, and session fixtures for supported behavior. Measure coverage of non-generated production code by public and transport package and in combination; enforce at least 80% combined coverage in CI and target 90%. Report generated-code exclusions and remaining uncovered behavior rather than adding tests solely to raise a number.
- [ ] **7.** Put the reusable public provider package under `pkg/<provider>`, generated provider wire models under `pkg/dependencymodels`, and transport behavior under `pkg/dependencies/<transport>`. Use distinct schema and generated Go files for each API responsibility, such as authentication, behaviors, devices, and feature payloads; a compatible shared Go package is allowed. Keep related request, response, and nested component definitions together rather than a monolithic model file or a second catch-all `internal/models` or `internal/wire` model bucket. Keep public semantic projections separate from provider wire contracts. Handwritten model companions may supply conversion or decoding behavior but must not redefine wire fields. Use compatibility aliases when moving existing exported types; when old field shapes differ, generate their compatibility definitions from a separate projection schema. Verify public import paths from a separate consumer module.
- [ ] **8.** Initialize clients through explicit functional options (for example `NewClient(WithBaseURL(...), WithHTTPClient(...))`) with sensible defaults and validation. Keep account credentials out of reusable client configuration when the client serves multiple accounts.
- [ ] **9.** Keep the reusable client stateless with respect to accounts and connections. Return explicit session objects for login, event streams, sockets, RTC, or other stateful lifecycles; make ownership, close, errors, and token state visible to callers.
   Include injected transport state in this audit. A shared `http.Client.Jar` must not
   transfer account cookies between sessions. Reject unsafe shared cookie jars with a
   distinguishable configuration error or keep cookie state in explicit sessions. Test
   two accounts through the same reusable client and assert complete outbound requests.
- [ ] **10.** Allow callers to inject the transport at every network edge the library uses, including HTTP, HTTP/2, WebSocket, MQTT, RTC signaling, and sockets opened by dependencies as applicable. A configurable concrete dialer is insufficient when it cannot substitute an offline connection; provide a connection-producing dial hook or equivalent seam and test the actual framed request and response through it without real credentials or network access.
- [ ] **11.** Expose supported token exchange and refresh as explicit operations that return the current credentials to the caller. Do not silently refresh or retain updated tokens inside a reusable client; document caller storage and renewal responsibilities. For providers without a supported refresh contract, document explicit reauthentication and credential retrieval using the supported login operation.
- [ ] **12.** Publish all customer-facing guides as MDX files under `docs/guides/` in the GitHub Pages site. Link guides to the matching generated reference pages. Keep separate repository Markdown only for contributor and release process notes; check internal links from **all** rendered pages, including the site root and generated references, and review external destinations and release-note links after a docs migration. Check schema-supplied links such as `externalDocs` even when they are loaded at runtime and absent from static HTML anchors. Verify the destination guide exists and renders its expected content; HTTP 200 alone can be a fallback error page.
- [ ] **13.** Before release, edit every published page for concise copy: remove repeated caveats, stale claims, and links to duplicate repository documents; keep each page's purpose, evidence status, and next action clear. Keep the README focused on installation, a short authenticated example, supported capabilities, caller configuration and lifecycle obligations, and links to user guides. Put wire inventories, generation details, fixture provenance audits, coverage mechanics, migration history, and reviewer evidence in contributor material, not the README or customer guide navigation. Delete obsolete internal reports and duplicate process documents; keep one current checklist and independent review record plus contributor instructions needed to maintain the library. Review every tracked documentation file for audience, purpose, duplication, and incoming links, then review the rendered Pages site. Check release-note copy and URLs against the published guide locations.
- [ ] **14.** Before signing off a library migration or release, have two independent reviewers who did not implement the change audit the library against every item in this checklist and the linked standards. Have both reviewers write their separate verdicts and concrete evidence for every numbered item in one current repository review document, including the reviewed commit, discrepancies, and the disposition of every finding; link it from the library's checklist. Keep this item unchecked while any finding or other checklist item remains open; recording or tracking a finding does not resolve it. Re-run affected checks and have both reviewers verify every fix at the final commit before checking this item. Do not accept an implementer's own checklist sign-off as independent verification.
    For items 4 and 7, attach the complete wire-model inventory and verify every entry against
    its generated definition and actual use. Search independently for types missing from the
    inventory; do not accept the implementer's generated-file list as the full population.
    For items 12 and 13, inspect the README and every tracked documentation file as well as
    rendered pages. A successful build or link check does not establish appropriate audience
    or absence of redundant internal documents. Missing inventory entries or unexamined files
    keep the corresponding verdict open.
- [ ] **15.** Store and replay each wire exchange as a paired request and response (or an ordered bidirectional message transcript). Include method, origin, escaped path, repeated query values, relevant headers, and body or frame payload in the request expectation; include response status, relevant headers, and body. Match the outbound request before returning its response, reject unexpected or duplicate calls, and assert that every expected exchange was consumed in order where order matters. Never fall back to a response when request matching fails. For HTTP client request matching, require an empty `RequestURI` and `URL.Fragment`; add negative tests for both. Represent volatile IDs, timestamps, signatures, and redacted credentials with explicit match rules that still validate their format or decoded meaning. Apply this to every supported transport and classify each pair as captured or synthetic; a response-only fixture does not satisfy replay verification.
     For HTTP, validate the effective authority, including any `Request.Host` override, before
     returning the paired response. Reject unexpected URL user information, opaque URLs, and
     malformed query strings; include a request-identity mismatch negative control. Keep secret
     values out of mismatch diagnostics.
    Match authentication forms and headers in full, including CSRF, OTP, and token exchange fields.
    Bind OAuth state to the callback, validate PKCE challenge/verifier relationships, and bind the
    hardware identity across requests. Validate volatile field formats before normalization.
    Match every signaling envelope and handshake. Check lifecycle completion after the expected
    close or teardown; a startup notification or terminal read timeout is not cleanup proof.

- [ ] **16.** Provide an installable standalone CLI that consumes the public SDK so customers can test the library without a consuming application. Use a separate module under `cmd/go-<provider>`; keep CLI concerns out of the SDK. Cover authentication and explicit token exchange, device or endpoint discovery, important read/control workflows, and event/session lifecycles where supported. Include useful help, machine-readable output, nonzero failures, cancellation, and session cleanup. Accept credentials through documented environment, stdin, or explicit file inputs; keep secrets out of arguments and ordinary output, and make credential export an explicit action. Require explicit commands for device changes. Document installation and customer examples in an MDX guide. Test CLI commands offline through injected paired request/response transports, including authentication errors and lifecycle cleanup, and run blocking pinned all-linter, build, test, and module checks for the CLI in CI. Verify a separate consumer installation from the published CLI module and release its module tags with the SDK.
    Make routine use a customer workflow: complete authorization, store credentials with
    explicit ownership, enumerate devices, select a device by ID, and expose useful reads
    and controls through named commands. Supply stable client identities, protocol defaults,
    and discovered device metadata automatically. Do not require request JSON, manual
    credential copying, or wire parameters for the ordinary login and device-control flow.
    Provide logout and document credential storage. Write the CLI guide as a short ordered
    sequence of copyable installation, login, discovery, and device-operation instructions.
    Explain how to choose discovered IDs; move advanced formats and protocol details to
    separate help or references. Verify the documented sequence offline, including failed
    authorization, device lookup, cancellation, and session cleanup.
    When supported by the provider, implement the complete interactive authorization flow,
    including browser consent, a localhost callback and provider-required activation calls.
    Expose composable SDK authorization mechanisms for caller-owned callback endpoints.
    Validate callback state and PKCE when supported; bind the exact redirect to the exchange.
    Keep listeners and temporary authorization state in an explicit cancellable lifecycle,
    with offline browser, listener and transport injection and idempotent cleanup. Document
    registered redirects, credential ownership and explicit export; manual code exchange alone
    is not end-to-end authorization.

See [verification](https://github.com/portpowered/go-third-party-template/blob/c6cfd7edf23924ba43ca994dbacabbf82ea85344/docs/verification.md), [client design](https://github.com/portpowered/go-third-party-template/blob/c6cfd7edf23924ba43ca994dbacabbf82ea85344/docs/client-design.md), and
[website publishing](https://github.com/portpowered/go-third-party-template/blob/c6cfd7edf23924ba43ca994dbacabbf82ea85344/docs/website.md) for the implementation details.
