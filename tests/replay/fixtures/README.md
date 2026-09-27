# Replay fixtures

All replay inputs live in this directory. Subdirectories group behavior first;
the final directory labels the evidence behind each case:

| Directory | Contents |
| --- | --- |
| `http/captured/` | Sanitized HTTP requests and responses from the network recording. `variants/` holds additional responses and request bodies for the same operations. |
| `signaling/captured/` | Ordered WebSocket application messages from two captured PTZ sessions. |
| `http/baseline/` | Inherited, sanitized response fixtures whose original capture date and account provenance are unavailable. |
| `auth/synthetic/`, `account/synthetic/`, `http/synthetic/`, `media/synthetic/`, `signaling/synthetic/` | Authored failure, edge, and compatibility cases. These are test inputs, not observations of the current service. |
| `schemas/` | JSON schemas used to validate captured fixture shape. |

Captured HTTP files retain method, origin, path, query, headers, body, status,
and response shape. Session files retain message order, direction, and JSON
payloads. Account, device, session, and network identifiers are replaced with
synthetic values while preserving identity relationships and value types. The
captured ticket route does not establish how it relates to the signaling socket.

The baseline fixtures were inherited with the client source. They are useful
for regression tests but do not certify that their routes still work. The
synthetic fixtures cover paths missing from the recording and must not be
presented as captured vendor behavior. Live integration tests remain separate.

Go replay tests load these files through `internal/testkit/replay`; protocol
and capture contract tests validate the captured files against OpenAPI,
AsyncAPI, and the schemas here. To regenerate captured fixtures from a private
mitmproxy dump, install `tools/capture/requirements.txt` and run
`python tools/capture/extract.py <capture-file>`. Never commit the source dump
or unredacted credentials, video, account details, or network addresses.
