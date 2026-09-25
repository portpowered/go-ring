# Offline porting verification

The selected library improvement scope is implemented and locally verified.
Execution follows the [reference-first process](internal/process-of-reverse-engineering.md):
Python behavior baseline, paired recording contracts, explicit Go test mappings,
implementation, then acceptance checks. The [parity matrix](parity-matrix.md) and
[porting progress](porting-progress.md) distinguish captured evidence, Python
comparison, synthetic regression cases, and unsupported features.

## Results

Verified locally on Windows with Go 1.24.2, `GOWORK=off`:

| Check | Result |
|---|---|
| `go run ./tools/coverage` | Passed, including full Go race suite; 1984/2202 maintained library statements, 90.10% |
| Per-package coverage floors | Passed; `internal/protocol` 100%, `internal/signaling` 96.7%, REST 92.3%, public `ring` 87.3%, API models 100% |
| `go vet ./...` | Passed |
| `go build ./...` | Passed, including examples |
| Go formatting / module tidy | Clean; no module metadata change |
| Pinned Python original tests | 40 passed |
| Python fixture replay, including the original-test migration index | 72 passed; 447/465 selected Python lines, 96.13% |
| OpenAPI/AsyncAPI document and payload checks | 11 passed |
| Capture extraction/sanitizer/schema checks | 11 passed |

`python tools/verify_reference.py` runs the original Python tests, fixture-only
coverage gate, protocol contracts, and capture checks. It
requires the initialized reference submodule, uv, and Node.js/npm; it installs
isolated test dependencies. The actual test replay is offline, with local
HTTP/WebSocket peers permitted. The private mitmproxy file is not required.
Coverage includes handwritten code under `pkg` and `internal`; test harnesses,
examples, tests, and maintainer tools are exercised but excluded from the
library denominator. Minor scheduling-dependent branch counts can vary; the
90% overall gate and per-package floors remain enforced.

## Delivered scope

- T0-T2: pinned, unmodified Python reference; source and divergence mappings;
  sanitized HTTP exchanges and ordered session recordings; strict replay
  harnesses and synthetic variants without capture metadata manifests.
- T3-T5: explicit client/connection/device-session ownership, preserved auth
  mechanisms, HTTP reliability and configuration regression tests, validated
  OpenAPI/AsyncAPI contracts. Wire DTOs remain handwritten with schema checks;
  [architecture](architecture.md) explains that decision.
- T6: typed settings and siren operations with recorded replay. Optional EVM
  history adapters, groups, favorites, deletion, reboot and other captured
  routes remain separate decisions, not implied public SDK support.
- T7: persistent signaling, caller SDP/ICE, independent identity domains, typed
  PTZ methods and results, heartbeat, bounded queues and priority writes,
  cancellation, best-effort movement stop and sixty-minute SDK expiry policy.
- T8-T9: Python adapters and documented divergences, runnable SDP examples,
  README/API/lifecycle/migration guidance, coverage and contract CI jobs.

Negative tests include malformed responses and SDP, wrong session identities,
missed heartbeats, RPC correlation and cancellation, blocked writes, queue
pressure, unexpected HTTP requests, regional endpoint overrides, injected
clients, failed token retrieval, and authentication failure stages. These are
synthetic reliability evidence, not additional captured vendor behavior.
The Go replay suite now uses the Python harness's portable ticket, legacy
control, in-home chime, recording-byte, HTTP-failure, and remote-session
fixtures. Captured PTZ commands, inbound ICE, and heartbeat pairs run as
individually named subtests. Python's snapshot and share URL variants have
no matching public Go API.

## Explicit limits

No live Ring account or hardware compatibility test was run. CI is configured
for additional operating systems and Go versions; those remote jobs were not
executed as part of this local verification. The caller owns the media peer;
Pion offer construction is tested locally, not end-to-end media interoperability.

Full Python feature parity is not the completion criterion. Public push and
playback abstractions remain deferred; existing event support is experimental.
The captured GET ticket endpoint has not been established as equivalent to the
legacy POST signaling bootstrap. Python's siren-on duration query intentionally
differs from the captured Go request. Sixty-minute expiry is an SDK limit, and
PTZ acknowledgement or best-effort stop is not proof of physical movement cessation.
