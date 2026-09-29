# Independent paired-replay review

Reviewed implementation commit: `c0d4367871935c16621edb9c4d7d547097ea695e`.
Criteria: **LIB-05** in `docs/standards/library.md` and item 15 of
`go-third-party-template/docs/library-standards.md`. The reviewer did not
implement the replay changes.

| Criterion | Verdict | Evidence |
| --- | --- | --- |
| LIB-05 / template item 15 | **Pass** | All 39 OpenAPI operations have complete synthetic HTTP pairs; all 17 AsyncAPI channels occur in stored transcripts driven by the production client or session. Strict peers check each request or frame before sending the next response and assert consumption. |
| Independent verification of this item | **Pass** | Local lint, check, affected race tests, npm audit, and CI run `36500816217` passed at the reviewed commit. |

## HTTP pairs

`TestCompletePairedOperationInventory` derives the 39-operation inventory from
`api/openapi.yaml` and counts complete request/response pairs only under the
synthetic fixture roots. It excludes inherited historical and reference files.
`TestSyntheticPairMetadataMatchesOpenAPI` checks the 38 new pairs' operation
IDs, routes, origins, and response statuses. The existing signaling-ticket pair
provides the remaining operation. `TestMissingOperationsPairedReplay` and
`TestAdditionalOperationsPairedReplay` invoke the generated HTTP client with
the stored pairs and call `AssertConsumed` for each strict transport. The
transport matches method, origin, escaped path, repeated query values, relevant
headers, and body before returning the stored status, headers, and body. Its
negative tests reject order changes, duplicate or unexpected calls, and
request mismatches. These pairs are hand-authored synthetic examples, not
provider captures.

## Signaling transcripts

Four stored production transcripts under
`tests/replay/fixtures/signaling/synthetic/paired/` cover the 17 AsyncAPI
channels: push and heartbeat, playback, and live session with PTZ. Tests in
`tests/replay/signaling_production_transcript_test.go` open a public
`ring.Client`, consume the stored signaling-ticket HTTP pair, then use
`SubscribePush`, `StartPlayback`, or `StartDeviceSession` and assert the public
event, answer, or control result. The push heartbeat test in
`pkg/ring/signaling_heartbeat_transcript_test.go` drives the production
subscription timer at a short test interval. Each test calls the scripted
peer's `AssertComplete` and the ticket transport's `AssertConsumed`.

`TestProductionSignalingChannelInventory` compares the union of those four
fixtures against every `api/asyncapi.yaml` channel and its send/receive
direction; `TestProductionSignalingTranscriptsMatchAsyncAPIChannels` validates
each stored frame against its channel payload schema. These inventories use the
same named files as the production tests. The separate 17-channel
`full-session.json` uses a generic scripted client and is explicitly excluded
from production-client coverage.

The WebSocket peer checks the upgrade method, Host, Origin, escaped path,
query, and configured headers, then checks complete ordered frames. Template
rules bind outbound UUIDs by format and require exact reuse in later frames;
the PTZ timestamp rule checks an epoch-millisecond number within one minute.
Unit tests reject invalid UUIDs, changed bindings, extra JSON keys, upgrade
mismatches, a trailing frame, and an outbound mismatch before any stored
response is sent. The peer accepts one connection and reports unused or
unexpected exchanges.

The earlier review at `09d3a56` found three gaps: 13 HTTP operations lacked
complete pairs; socket handshake and full-flow coverage were partial; and
inherited files labeled `captured` lacked provenance. The 39-operation pair
gate resolves the first. Strict upgrade expectations and production-driven
stored transcripts resolve the second. The inherited files now live under
`historical/`; the fixture guide states their source and UTC capture dates are
unknown and excludes them from current-provider evidence. The interim
`bf78442` review also found that its 17-channel transcript was exercised only
by a raw scripted client; the four production transcripts resolve that gap.

## Verification

- `make lint`: pass, zero issues.
- `make check`: pass, including build, fixture/schema contracts, full Go tests,
  CLI tests, and vet. Protocol dependencies reported zero vulnerabilities.
- `go test -count=1 -race ./internal/testkit/replay ./tests/replay ./pkg/ring -timeout 120s`:
  pass.
- `npm audit --prefix tools/protocols --audit-level=moderate`: pass, zero
  vulnerabilities.
- [CI run 36500816217](https://github.com/portpowered/go-ring/actions/runs/36500816217):
  pass at `c0d4367`, including fixture contracts, schema generation, lint,
  coverage, compatibility, and Linux/macOS/Windows race jobs.

**Signoff:** LIB-05 and template item 15 pass at the reviewed commit. This is
synthetic compatibility evidence; it does not assert current provider behavior
or turn historical files into verified captures. The pre-existing untracked
executable and coverage file were left untouched.
