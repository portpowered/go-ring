# Independent paired-replay review

Reviewed commit: `8f169c0e770690cc3b5d240d4d364235ac33d1f6`.
Criteria: **LIB-05** in `docs/standards/library.md` and item 15 of
`go-third-party-template/docs/library-standards.md`. This report reviews
evidence only; it does not sign off the implementation.

| Criterion | Verdict | Evidence |
| --- | --- | --- |
| LIB-05 / template item 15 | **Open** | Paired HTTP and ordered signaling cassettes exist, but order, socket-upgrade and terminal-frame matching, volatile-field rules, provenance, and complete operation coverage remain unverified or incomplete. |
| Renewed independent signoff | **Open** | Recheck this standard and the rest of the checklist after repairs at the final commit. |

## Evidence and gaps

1. **Actual pairs versus model inputs.** All 30 JSON files under
   `tests/replay/fixtures/http/captured/`, including its `variants/`
   directory, have `request` and `response` objects. The two files under
   `signaling/captured/` contain ordered, directed application frames.
   `internal/testkit/replay.Transport` matches HTTP method, origin, escaped
   path, repeated query values, configured headers, and body before returning
   a fresh response; it marks the cassette used and `AssertConsumed` reports
   unused or unexpected calls. The WebSocket script compares expected text
   JSON or binary frames in sequence and has `AssertComplete`. The 15
   inherited `http/baseline/` files are response-only historical model
   inputs, and `push/` holds authored event examples; neither is an HTTP or
   socket replay cassette. `http/reference/` has source-derived pairs, but
   they are not provider captures.
2. **HTTP order is not enforced.** `Transport.RoundTrip` scans every unused
   cassette and serves whichever request matches. If a workflow supplies
   exchanges A then B, the client can issue B then A and consume both, so
   `AssertConsumed` succeeds despite reversed order. LIB-05 requires order
   where it matters, notably authentication and dependent session setup.
   The testkit needs an ordered mode or explicit dependency/order assertion
   for such workflows.
3. **Socket replay starts after an unchecked upgrade.**
   `NewWebSocketServer` accepts the first upgrade request without validating
   method, origin, escaped path, query, or relevant authorization headers.
   It finishes successfully as soon as the last scripted frame is sent or
   received. An extra client frame after that step is not read or rejected;
   `AssertComplete` can therefore pass without proving transcript exhaustion
   at the connection boundary. Tests that use `LoadSessionRecording` often
   select individual messages for a behavior rather than replaying the whole
   recorded flow, as `internal/testkit/replay/recording.go` documents.
4. **No explicit volatile/redacted matcher.** HTTP cassette fields are exact
   strings or semantic JSON. The captured tests replace path placeholders
   and supply fixed synthetic tokens, but there is no reusable rule to
   verify the format or decoded meaning of a changing token, timestamp,
   signature, route ID, SDP identifier, or redacted credential. Tests that
   need a dynamic value cannot currently prove it conforms to a captured
   request expectation without editing that expectation or bypassing the
   cassette.
5. **Coverage and provenance need a checked inventory.** OpenAPI lists 39
   HTTP operations. The 30 captured files include variants of the same
   operations; synthetic and reference pairs cover additional paths, while
   many tests construct inline responses or model inputs. There is no
   operation-by-operation gate showing that every supported HTTP operation
   and socket exchange is exercised by a consumed pair or transcript.
   `docs/developer-facing/replay-format.md` also states that the files
   labeled `captured` lack capture timestamps and extraction metadata.
   The shared verification standard requires known capture date and source
   for that classification; supply provenance or reclassify evidence whose
   capture origin cannot be established.

`make lint` passed. The first `make check` attempt was blocked by the
checkout's Git ownership guard during Go VCS stamping. Repeating it with
a process-local `safe.directory` setting passed build, protocol checks,
package tests, and vet. Unrelated local executable and coverage files were
left untouched.

**Signoff:** LIB-05 and template item 15 remain open at this commit.
