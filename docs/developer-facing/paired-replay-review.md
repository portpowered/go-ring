# Independent paired-replay review

Reviewed implementation commit: `09d3a563cd251b09e8ef14f73f9a2d6e5a62b45c`.
Criteria: **LIB-05** in `docs/standards/library.md` and item 15 of
`go-third-party-template/docs/library-standards.md`. This review changes no
replay implementation.

| Criterion | Verdict | Evidence |
| --- | --- | --- |
| LIB-05 / template item 15 | **Open** | Ordered HTTP and signaling replay boundaries are stronger, but the stored-pair inventory and captured provenance remain incomplete. |
| Renewed independent signoff | **Open** | Recheck every library criterion after the remaining findings are repaired. |

## Verified improvements

`internal/testkit/replay.NewTransport` now consumes HTTP cassettes in order.
`NewUnorderedTransport` is an explicit choice for independent requests.
`RoundTrip` matches method, origin, escaped path, repeated query values,
headers, and body before serving a fresh response; it rejects an unmatched
or duplicate call, and `AssertConsumed` reports unused or unexpected calls.
The testkit checks out-of-order and duplicate cases. `NewWebSocketServer`
now checks upgrade method, escaped path, query, and configured application
headers, and reads once after the final scripted step to reject a trailing
client frame. `AssertComplete` persists the result. The 30 HTTP JSON files
under `http/captured/` have request and response objects; two signaling
files contain directed application frames. Historical baseline responses,
source-derived reference pairs, and authored synthetic inputs remain
separately described in the fixture guide.

## Open findings

1. **No complete operation-to-pair gate.** OpenAPI declares 39 HTTP
   method/path operations. A recursive inventory of stored JSON objects with
   both `request` and `response` found 45 pairs across the fixture tree,
   covering only 26 distinct schema method/path entries. Thirteen have no
   stored JSON pair: `beginOrContinueOAuthAuthorization`,
   `submitOAuthCredentials`, `verifyOAuthTwoFactorCode`,
   `exchangeOrRefreshOAuthToken`, `registerClientSession`,
   `turnFloodlightOff`, `getLegacyDeviceHealth`,
   `getLegacyDeviceHistory`, `getActiveDings`, `streamRecording`,
   `getLegacyRecordingShareURL`, `refreshLegacySnapshotTimestamp`, and
   `getLegacySnapshotImage`.
   Some have inline response tests, but those are not a checked stored pair
   with full outbound expectations. Array-based synthetic cases can also
   omit origin or headers and acquire them in test code. Add a checked
   inventory that maps every supported operation to a consumed, complete
   pair and distinguishes any operation deliberately unsupported.
2. **Socket handshake and recorded-flow coverage remain partial.** The
   scripted peer checks path/query/configured headers, but
   `websocket.Upgrader.CheckOrigin` always returns true and `WSHandshake`
   has no expected origin/host field. The two captured signaling files hold
   application frames without a paired upgrade expectation. Tests often
   select particular messages from `LoadSessionRecording` rather than
   consuming an entire recorded flow. Record and check the relevant upgrade
   fields and map each supported socket exchange to an exhaustive ordered
   transcript. The new trailing-frame check closes the earlier immediate
   extra-frame gap; it does not supply missing capture steps.
3. **“Captured” lacks verifiable provenance.** The 30 HTTP files and two
   signaling files are still labeled `captured`, while
   `docs/developer-facing/replay-format.md` says they have no capture dates,
   environment labels, extraction metadata, or provenance digests. The
   shared verification guide requires a known source and UTC capture date
   before that label is used. Add those records or reclassify the files as
   historical/synthetic; do not use them as current provider evidence until
   provenance is established.

Fixed synthetic token and identifier values in current cassettes match
exactly. This is an explicit synthetic matching policy, not provider-format
validation; any newly variable or redacted field needs its own format or
decoded-value rule before it counts as replay evidence.

`make lint` passed. The first `make check` run failed once in
`TestRecordedPublicPushSubscriptionAndEvent` with `signaling send failed`;
ten targeted repeats passed, then a second full `make check` passed. Fresh
`go test -count=1 -race ./internal/testkit/replay ./tests/replay -timeout 120s`
also passed. The intermittent failure should be tracked separately from the
LIB-05 inventory and provenance gaps. The pre-existing untracked executable
and coverage file were left untouched.

**Signoff:** LIB-05, template item 15, and renewed independent signoff remain
open.
