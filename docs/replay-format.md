# Recording and replay format

Proposed format v1, to implement with the replay harness. Examples below specify concrete file shapes; they are illustrative sanitized scenarios, not already-extracted fixtures. C1 identifies the real native recording in the plan. HTTP exchanges and WebSocket messages retain separate evidence references. No test connects to recorded production addresses.

## Artifact layout

```text
test/fixtures/
  captures/c1/manifest.yaml
  captures/c1/http/device-list/exchange.yaml
  captures/c1/http/device-list/response.json
  captures/c1/sessions/live-ptz/transcript.jsonl
  captures/c1/sessions/live-ptz/offer.sdp
  captures/c1/sessions/live-ptz/answer.sdp
  captures/c1/sessions/live-ptz/messages/*.json
  legacy/...
  synthetic/heartbeat-loss/...
test/replay/
  scenarios/http/device-list.yaml
  scenarios/session/live-ptz.yaml
  scenarios/system/device-session.yaml
  expectations/device-list.json
  expectations/live-ptz.json
  schemas/manifest.schema.json
  schemas/exchange.schema.json
  schemas/transcript-entry.schema.json
  schemas/scenario.schema.json
tools/capture/                 # native-flow/HAR readers and sanitizer
internal/testkit/replay/       # Go matcher, local servers, fake clock
tools/reference-replay/       # optional Python adapter, outside submodule
```

Raw native recordings/HAR files stay local and untracked. Public cassettes contain minimal, sanitized operation-specific data. Keep recordings (what happened), scenarios (how to drive the SDK), and expectations (what correct public behavior means) separate. Never derive the expected result by invoking the same implementation under test.

## Manifest

```yaml
format_version: 1
capture_id: c1
source:
  format: mitmproxy
  sha256: 2b76185876adac6487db378ce0a340060e19171049091dce6c3c3559b8f70b63
  captured_at: null             # unknown until source metadata is checked
  environment: {region: unknown, account_role: unknown}
extraction:
  tool_version: capture-normalizer-v1
  redaction_profile: ring-v1
  reviewed: false               # must become true before publication
  review_reference: null
artifacts:
  - path: http/device-list/exchange.yaml
    evidence: {kind: captured, flow_ordinals: [EXTRACTOR_ASSIGNED_INTEGER]}
    sha256: GENERATED_ARTIFACT_DIGEST
  - path: sessions/live-ptz/transcript.jsonl
    evidence: {kind: captured, flow_ordinals: [21]}
    sha256: GENERATED_ARTIFACT_DIGEST
```

Uppercase placeholders are specification placeholders only; actual manifests require concrete typed values/digests and reject placeholders. Flow/message ordinals are one-based. For a transcript subset, list every retained original ordinal, not a claim that omitted messages never occurred. Native-file digest plus ordinals lets a maintainer find the raw source privately. Relative times start at zero; keep original time precision in private extraction metadata if needed, not personal session chronology in public fixtures.

## Raw HTTP cassette shape

```yaml
format_version: 1
id: c1-device-list-001
evidence: {capture_id: c1, flow_ordinal: EXTRACTOR_ASSIGNED_INTEGER}
request:
  method: GET
  origin: https://api.ring.com
  path: /device_info/v3/devices
  query: []                    # ordered name/value pairs, permits repeated keys
  headers:
    accept: [application/json]
    authorization: [Bearer fixture-access-token]
  body: {encoding: none}
response:
  status: 200
  headers: {content-type: [application/json]}
  body: {encoding: json, file: response.json}
timing: {response_delay_ms: 0}
```

The actual extractor retains observed fields, including whether Accept was present; the example does not assert those headers were in C1. Origin is a logical match key, never a dial destination. Headers are lowercased maps of value arrays; query parameters are arrays of `{name, value}` pairs. Paths retain synthetic IDs consistently across fixtures. Represent absent body as `encoding: none`; empty text, `{}`, `[]` and JSON null are distinct. Bodies use `json`, `utf8`, or `base64` plus a file or inline value (exactly one). Binary fixtures use generated non-private payloads explicitly marked synthetic. Body hash validation occurs before replay.

Absent response is `response: null` with `outcome: capture_incomplete`; it cannot be replayed as 200 or promoted to a vendor timeout. A timeout/error variant is a separate synthetic case with `derived_from`. Preserve observed redirects/statuses. Decode compression before sanitizing and recalculate content length/encoding metadata for the sanitized replay representation; record transformations in the manifest. Don't replay stale Content-Length or compressed-body headers against changed bytes.

```yaml
format_version: 1
id: http-device-list
kind: http
subject: Client.ListDevices
setup: {token: fixture-access-token}
steps:
  - call: {id: list, operation: ListDevices, args: {}}
  - exchange:
      file: ../../../fixtures/captures/c1/http/device-list/exchange.yaml
      match:
        method: exact
        origin: exact
        path: exact
        query: exact_multimap
        headers: {required: [authorization], ignored: [user-agent]}
        body: semantic_json
  - await: {call: list, expectation: ../../expectations/device-list.json}
assertions: {all_exchanges_consumed: true, unexpected_requests: fail}
```

`call` starts a call without blocking scenario execution; `await` joins it. Match modes must be explicit and validated. Header ignores need a rationale; production auth must still be asserted using fixture credentials. semantic_json compares structure/numeric values without float64 precision loss and ignores object key order, not missing fields. Arrays remain ordered. Exact multimap query matching retains repeated values; endpoint-specific order sensitivity can select ordered_pairs. Each exchange is consumed once and allocates a fresh response/body. Repeats require a count; broad URL fallback fixtures are prohibited.

## Session recording shape

Store WebSocket transcript entries in JSONL; large bodies reference adjacent files. A representative pair from C1 flow 21 messages 30/31 looks like this after consistent sanitization (offsets are illustrative):

```jsonl
{"seq":1,"source":{"flow":21,"message":30},"offset_ms":0,"direction":"client_to_server","frame":"text","message_file":"messages/pan-step.json"}
{"seq":2,"source":{"flow":21,"message":31},"offset_ms":20,"direction":"server_to_client","frame":"text","message_file":"messages/pan-result.json"}
```

`pan-step.json`:

```json
{"method":"rpc","dialog_id":"dialog-1","riid":"route-1","body":{"doorbot_id":1001,"session_id":"signal-session-1","command":{"jsonrpc":"2.0","id":"command-1","method":"PTZ.Pan.Step","params":{"direction":"LEFT","sessionId":"control-session-1","timestamp":1700000000000,"version":1}}}}
```

`pan-result.json`:

```json
{"method":"rpc","dialog_id":"dialog-1","riid":"route-1","body":{"doorbot_id":1001,"session_id":"signal-session-1","command":{"jsonrpc":"2.0","id":"command-1","result":{"sessionId":"control-session-1","timestamp":2700000000000,"version":1}}}}
```

These values are synthetic replacements, preserving the distinct identity domains and numeric timestamp shape. Verify actual riid/correlation relationships during extraction; do not replace all identifiers with one shared redaction string. Transcript entries can also represent binary frames, WebSocket control ping/pong, close code/reason and disconnect. Application JSON ping/pong remain text messages. HTTP upgrade request/response is its own cassette linked by connection ID, including sanitized query token matching; do not discard handshake behavior.

A complete live-session fixture includes bootstrap, offer/answer, activation and readiness messages, PTZ exchanges, heartbeat and closure. An isolated PTZ pair is a component test, not proof that session startup works. Preserve source order and message types; transcript extraction must report unsupported or omitted frames rather than silently dropping them.

## Session scenario and matching

```yaml
format_version: 1
id: session-pan-step
kind: session
clock: {mode: manual, initial_unix_ms: 1700000000000}
ids: {dialog: dialog-1, control_session: control-session-1, command: command-1}
setup:
  connection: local-scripted-websocket
  session: active_fixture      # explicit component precondition
  device_id: "1001"
  signaling_session_id: signal-session-1
steps:
  - call: {id: pan, operation: DeviceSession.PanStep, args: {direction: LEFT}}
  - expect_client:
      file: ../../../fixtures/captures/c1/sessions/live-ptz/messages/pan-step.json
      match: semantic_json
  - send_server:
      file: ../../../fixtures/captures/c1/sessions/live-ptz/messages/pan-result.json
  - await: {call: pan, expectation: ../../expectations/live-ptz.json}
  - advance_clock: {milliseconds: 10000}
  - expect_client:
      json: {method: ping, body: {doorbot_id: 1001, session_id: signal-session-1}}
      match: subset_json
      rationale: "Heartbeat routing asserted separately; generated envelope IDs checked by bindings."
assertions:
  pending_rpc_count: 0
  unexpected_messages: fail
```

The interpreter also supports named barriers, bounded unordered groups, cancel_call, cancel_session, connection_failure and assert_state. Avoid unconditional sleeps: wait for outbound-message barriers, then advance fake time. Capture offsets are observations, not instructions to wait real minutes. Time conversion is explicit (`milliseconds` in scenarios, documented conversion of vendor heartbeat seconds).

For IDs not injected, use `bind` on the first expected outbound JSON pointer and `equals_binding` thereafter. Validate format/type and independence of different ID domains. Bindings are per connection/session scope and reset between cases. Timestamps use an injected clock or an explicit tolerance matcher; no global “ignore all IDs/times” normalization. Unordered groups list exactly which messages may commute; PTZ commands, activation prerequisites and causality cannot be globally sorted.

Synthetic variants reference their parent transcript and list edits: dropped pong, swapped replies, duplicate result, malformed SDP, wrong session ID, blocked write, canceled call. A fake-clock 60-minute expiry case is synthetic SDK-policy coverage, not a captured vendor disconnect.

## Whole-system tests

System means the complete public Go client against local HTTP + WebSocket peers, not live Ring. Compose scenarios rather than bypassing session initialization:

```yaml
format_version: 1
id: system-live-view-ptz
kind: system
network: local_only
clock: {mode: manual}
setup:
  credentials: fixture_access_token
  endpoints: local_server_profile
steps:
  - include: ../http/device-list.yaml
  - include: ../session/bootstrap-and-negotiate.yaml
  - include: ../session/ptz-and-heartbeats.yaml
  - include: ../session/close-and-drain.yaml
assertions:
  owned_connections: 0
  owned_sessions: 0
  pending_rpc_count: 0
  active_timers: 0
  all_exchanges_consumed: true
  unexpected_requests: fail
```

Includes operate in an explicit shared case context and export named connection/session handles; fixture-local ID bindings remain scoped. Bootstrap cases use the actual existing auth/session and ticket path required by the selected profile, not every observed alternate endpoint. Final assertions include expected typed public values, error categories and resource ownership. Future Pion integration tests can create two local peers for offer/answer validity; remote media success remains a separate opt-in live check.

## Validation and evidence priority

Validate files against versioned schemas; reject unknown scenario operators, traversal outside fixture roots, unresolved includes/placeholders, missing hashes/references, duplicate sequence IDs and inconsistent identity bindings. All fixtures undergo redaction review and secret scanning. Golden updates are explicit reviewed changes, never autoaccepted test output.

Prefer verified working Go behavior and direct recording evidence over Python when they diverge. Python comparison is an advisory parity report, not an equality gate. A synthetic mock passing is not proof of vendor behavior; distinguish that from a successful observed exchange. Where different versions/accounts legitimately differ, retain both profiles with scoped evidence. The divergence record includes competing shapes, chosen behavior, reason, source/fixture links and regression tests. A reviewed intentional divergence must pass CI without altering Go to imitate Python.
