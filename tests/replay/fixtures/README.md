# Replay fixtures

All replay inputs live in this directory. Subdirectories group behavior first;
the final directory labels the evidence behind each case:

| Directory | Contents |
| --- | --- |
| `http/historical/` | Inherited sanitized HTTP pairs without verifiable source and UTC capture dates. `variants/` holds additional response and request-body cases. They are historical references, not current provider evidence. |
| `signaling/historical/` | Inherited ordered WebSocket application messages from two sessions without verifiable source and UTC capture dates. |
| `signaling/synthetic/paired/` | Hand-authored complete WebSocket upgrade and bidirectional transcript. Its 17 frames cover all AsyncAPI channels and are validated against the channel payload schemas; it is not provider evidence. |
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

Go replay tests load these files through `internal/testkit/replay`; protocol
and fixture contract tests validate the historical files against OpenAPI,
AsyncAPI, and the schemas here. A future real capture requires a known source
and UTC capture date before it can use a `captured/` directory. The extractor
can produce candidate files with `go run ./tools/capture <capture-file>`;
review provenance and sanitization before committing them. Never commit the source dump
or unredacted credentials, video, account details, or network addresses.
