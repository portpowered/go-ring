# Recorded HTTP and signaling fixtures

The checked-in recordings under `tests/replay/fixtures` contain sanitized HTTP
exchanges and ordered WebSocket application messages. They preserve observed
request/response fields, message directions, and JSON bodies. They do not carry
manifests, provenance digests, capture timestamps, environment labels, or extraction
metadata. Synthetic identity replacements are consistent within each session.

## HTTP exchange JSON

Each `tests/replay/fixtures/http/captured/*.json` file has this shape:

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

Each `tests/replay/fixtures/signaling/captured/*.json` file contains an ordered `messages`
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

`api/openapi.yaml` distinguishes captured HTTP operations from existing and
synthetic-replay HTTP/auth operations; `api/asyncapi.yaml` describes observed
signaling envelope methods and PTZ RPC shapes. `tests/replay/contracts_test.go`
checks recorded HTTP method/path/status/origin and signaling method constants.
The Go tests in `tools/protocols` validate recorded payloads against the
schemas and validate the full OpenAPI and AsyncAPI documents. `tests/replay/contracts.md` records historical
test mappings and labels signaling and other route-only additions as
capture-only. See that file before treating a captured route as proof of
equivalent high-level behavior.

## Repeatable verification order

Run the Go replay suite, then `make test-contracts` for independent protocol
and capture contract tests. CI installs Node.js dependencies for the AsyncAPI
validator, then runs the Go tests.

Next run `go test -race ./... -timeout 120s` with `GOWORK=off`, followed by
`make test-cover` to measure replay coverage, co-located unit coverage, and
their combined 90% handwritten-library budget independently. See the
[coverage guide](coverage.md). Neither command
requires the private native recording: committed sanitized files are the test
inputs. Extraction of a new recording is a separate maintainer operation.
Passing these commands is one completion gate; the remaining feature mapping
and README/API documentation must also be satisfied before calling the
implementation complete.

## Portable replay

Additional JSON inputs under each feature's `synthetic/` directory are cases
for legacy routes and failures absent from the capture. They are not capture
observations. Go reads the JSON files directly. See the
[fixture guide](../../tests/replay/fixtures/README.md) for the full layout.
