# Recorded HTTP and signaling fixtures

The checked-in recordings under `test/recordings` contain sanitized HTTP
exchanges and ordered WebSocket application messages. They preserve observed
request/response fields, message directions, and JSON bodies. They do not carry
manifests, provenance digests, timestamps, environment labels, or extraction
metadata. Synthetic identity replacements are consistent within each session.

## HTTP exchange JSON

Each `test/recordings/http/*.json` file has this shape:

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

Query is an array of name/value objects and can retain repeated keys. JSON
bodies are stored as JSON values with `json: true`; raw bodies are stored as
strings with `json: false`, and `null` represents no body in that mode. JSON
null is distinct and replays as the four byte JSON value `null` when `json` is
true. HTTP path and origin are match keys only: replay always returns a local
response and never dials the recorded host.

`headers_mode` can be `exact` or `required`; omission means `exact`. Exact mode
requires the full header map to match. Required mode requires every recorded
header name and all its values to match, while allowing additional request
headers. Names are case-insensitive and repeated values are preserved. Method,
origin, escaped path, query multimap, and body always match strictly. JSON
comparison ignores object key order, preserves array order, compares numeric
values without float precision loss, and rejects trailing JSON values.

Use `replay.LoadExchange`, `replay.NewTransport`, and
`Transport.AssertConsumed` from `internal/testkit/replay`. Each cassette is
consumed once, each response receives a fresh body, and an unmatched request
causes the test's final consumed assertion to fail.

## WebSocket session recordings

Each `test/recordings/sessions/*.json` file contains an ordered `messages`
array. Entries have `direction`, `frame`, and structured `payload` fields. The
current captured application messages are text JSON. The testkit script uses
`WSStep{Kind, Frame, Body}`: map `client_to_server` to `expect`,
`server_to_client` to `send`, and marshal the payload into `Body`. Text frames
are compared as semantic JSON. Binary frames compare byte-for-byte. The local
script server accepts one connection, applies bounded read/write deadlines,
and exposes `AssertComplete` and `Close` for deterministic completion and
cleanup.

The transcripts include application heartbeat ping/pong, signaling setup and
close messages, and nested PTZ RPC methods. They are not proof of unrecorded
handshake variants or remote media success. Runtime heartbeat intervals,
expiry, retry, and cancellation rules remain SDK policy unless directly
captured. HTTP PTZ routes are not part of these recordings or specifications.

## Contract documents and baseline pairing

`api/openapi.yaml` describes only captured HTTP operations;
`api/asyncapi.yaml` describes observed signaling envelope methods and PTZ RPC
shapes. `test/contracts/contracts_test.go` checks the recordings against those
documents. `test/contracts/README.md` identifies Python baseline test
counterparts and labels signaling and other route-only additions as
capture-only. See that file before treating a captured route as proof of
equivalent high-level behavior.

## Repeatable verification order

Run `python tools/verify_reference.py` from the repository root (requires uv).
It checks the pinned Python suite first, then the shared-recording adapters,
then signaling payload schemas and recording/sanitizer tests. The reference
and capture tooling use separate ignored virtual environments so mitmproxy
cannot replace the reference's locked dependencies. The same command runs in
CI on Windows and Linux. It needs the reference submodule and package downloads
during setup; the reference tests themselves prohibit external connections.

Next run `go test -race ./... -timeout 120s` with `GOWORK=off`. Neither command
requires the private native recording: committed sanitized files are the test
inputs. Extraction of a new recording is a separate maintainer operation.
Passing these commands is one completion gate; the remaining feature mapping,
README/API documentation, and the planned 90% library coverage gate must also
be satisfied before calling the implementation complete.
