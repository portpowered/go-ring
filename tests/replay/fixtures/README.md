# Replay fixtures

All replay inputs live in this directory. Subdirectories group behavior first;
the final directory labels the evidence behind each case:

| Directory | Contents |
| --- | --- |
| `http/historical/` | Inherited sanitized HTTP pairs without verifiable source and UTC capture dates. `variants/` holds additional response and request-body cases. They are historical references, not current provider evidence. |
| `signaling/historical/` | Inherited ordered WebSocket application messages from two sessions without verifiable source and UTC capture dates. |
| `signaling/synthetic/paired/` | Hand-authored WebSocket examples. `full-session.json` is a 17-channel schema illustration exercised by a generic socket and does not count as production-client replay. The four `*-production.json` transcripts exercise the public client and its push, playback, and live sessions, including ticket bootstrap and upgrade. None is provider evidence. |
| `http/baseline/` | Inherited, sanitized response fixtures whose original capture date and account provenance are unavailable. |
| `http/reference/` | Source-derived request contracts for intercom unlock and FCM registration/subscriptions. These are not claims of captured Ring traffic. Each fixture names its source. |
| `http/synthetic/paired/` | Thirty-eight hand-authored, schema-shaped HTTP request/response pairs. They use fixed synthetic credentials, identifiers, and response values; they are not copied captures or provider observations. A separate synthetic signaling-ticket pair covers the thirty-ninth OpenAPI operation. |
| `push/` | Authored FCM notification examples used to test typed event extraction; live event payloads have not yet been captured. |
| `auth/synthetic/`, `account/synthetic/`, `http/synthetic/`, `media/synthetic/`, `signaling/synthetic/` | Authored failure, edge, and compatibility cases. These are test inputs, not observations of the current service. |
| `schemas/` | JSON schemas used to validate historical fixture shape. |

Historical HTTP files retain method, origin, path, query, headers, body, status,
and response shape. Session files retain message order, direction, and JSON
payloads. Account, device, session, and network identifiers are replaced with
synthetic values while preserving identity relationships and value types. The
historical ticket route does not establish how it relates to the signaling socket.

The baseline fixtures were inherited with the client source. They are useful
for regression tests but do not certify that their routes still work. The
synthetic fixtures cover all OpenAPI operations and must not be presented as
captured vendor behavior. Live integration tests remain separate.

The OpenAPI inventory gate counts only complete pairs under `http/synthetic/`
and `auth/synthetic/`. It excludes `historical/`, `baseline/`, and `reference/`
from item-15 coverage. Generated-client replay consumes and checks every new
pair; the existing signaling-ticket replay consumes the remaining pair.
The separate AsyncAPI inventory gate requires every channel in the synthetic
signaling transcript and checks each frame against its channel schema.
The production transcripts cover all 17 AsyncAPI channels through public
client/session flows, plus the push heartbeat through its production timer
method at a test-only interval. A separate inventory test rejects missing
channels. They match complete outbound JSON, bind generated UUIDs by format
and exact reuse, validate PTZ timestamps within a one-minute window, inject
only stored responses, and check public results. Server-to-client channels
(subscription acknowledgment, push event, SDP, notification, pong, and session
created) are receive-only and are checked as sent frames and public results.

Go replay tests load these files through `internal/testkit/replay`; protocol
and fixture contract tests validate the historical files against OpenAPI,
AsyncAPI, and the schemas here. A future real capture requires a known source
and UTC capture date before it can use a `captured/` directory. The extractor
can produce candidate files with `go run ./tools/capture <capture-file>`;
review provenance and sanitization before committing them. Never commit the source dump
or unredacted credentials, video, account details, or network addresses.

## Fixture format

An HTTP pair contains a `request` and a `response`. Requests record `method`,
`origin`, escaped `path`, `query`, `headers`, `body`, and whether `body` is
JSON. Query is a list of name/value entries so repeated keys remain distinct.
JSON bodies are stored as JSON values; raw bodies are strings. `null` with
`json: false` means no body, while `null` with `json: true` is the JSON value
`null`. Responses record status, headers, body, and the JSON flag. Replays
always return a local response and never dial the recorded origin.

```json
{
  "request": {
    "method": "GET",
    "origin": "https://api.ring.com",
    "path": "/device_info/v3/devices",
    "query": [],
    "headers": {"Accept": ["application/json"]},
    "headers_mode": "required",
    "body": null,
    "json": false
  },
  "response": {
    "status": 200,
    "headers": {"Content-Type": ["application/json"]},
    "body": {"devices": []},
    "json": true
  }
}
```

`headers_mode` defaults to `exact`; `required` checks each recorded header and
value while allowing additional headers. Header names are case-insensitive.
Method, origin, escaped path, query values, and body match strictly. JSON
object key order is ignored, array order is preserved, numbers are compared
without float precision loss, and trailing JSON values are rejected. Ordered
transports consume exchanges in fixture order. Use an unordered transport only
when the requests are independent and order does not matter. Both reject
unexpected or duplicate requests; tests must assert that every exchange was
consumed. Variable fields need explicit format or decoded-value match rules.

Signaling session files contain an ordered `messages` array with `direction`,
`frame`, and structured `payload`. Text frames compare as semantic JSON and
binary frames compare byte-for-byte. The local script server checks the
WebSocket upgrade and configured headers, applies bounded deadlines, rejects
unexpected frames, and exposes completion and cleanup checks. A
`client_to_server` message is an expected client frame; a `server_to_client`
message is sent to the client. Synthetic tests that use a separate handshake
must keep that handshake expectation next to the transcript.

The replay behavior rules live in the
[library standard](../../../docs/standards/library.md); operation-specific
schema and evidence limits are in [API notes](../../../api/README.md). Run
`make test-contracts` to validate fixture and schema contracts. Run
`make test-race` for race-detected behavior; see [contributing](../../../CONTRIBUTING.md)
for the complete check list.
