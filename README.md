# go-ring

[![CI](https://github.com/portpowered/go-ring/actions/workflows/ci.yml/badge.svg)](https://github.com/portpowered/go-ring/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/portpowered/go-ring.svg)](https://pkg.go.dev/github.com/portpowered/go-ring)
[![License](https://img.shields.io/github/license/portpowered/go-ring)](LICENSE)

A Go client for Ring authentication, devices, controls, recordings, and persistent
signaling sessions. The library is being improved against a pinned
[Python reference](reference/python-ring-doorbell) and sanitized network
recordings. See the [porting progress](docs/porting-progress.md) and
[feature parity matrix](docs/parity-matrix.md) for verified behavior and gaps.

## Install

Requires Go 1.24 or later. Normal Go consumers do not need Python or the reference
submodule. The new signaling APIs are under development in this checkout; select
a release or commit that contains the API you use.

```sh
go get github.com/portpowered/go-ring
```

## List devices

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/portpowered/go-ring/pkg/ring"
)

func main() {
    client, err := ring.NewClient(ring.WithAccessToken(os.Getenv("RING_ACCESS_TOKEN")))
    if err != nil { log.Fatal(err) }
    defer client.Close()

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    devices, err := client.ListDevices(ctx)
    if err != nil { log.Fatal(err) }
    fmt.Println(len(devices.Doorbells))
}
```

The [device session example](examples/device-session) generates its SDP with Pion.
The [examples](examples) compile with the library. Running them contacts Ring and
may operate a device; normal tests do not run them.

For a fresh still image, call `GetSnapshot(ctx, ring.GetSnapshotRequest{DeviceID: id})`.
It triggers and polls Python's legacy snapshot route, then returns image bytes,
timestamp, and content type. The default is three immediate polls; set
`PollInterval` and a bounded context when the service needs time to produce a
new image. `ErrSnapshotNotReady` means no newer timestamp appeared. Recording
media remains a caller-closed stream through `GetRecording`; use
`GetRecordingShareURL` when you need the legacy share/play URL. These snapshot
and share routes are backed by synthetic Python replay, not a successful C1
capture. See [HTTP contract evidence](docs/protocols/http.md).

## Authentication

Existing authentication mechanisms remain supported: `Request2FACode`,
`Authenticate` with an optional OTP, `RefreshToken`, `WithAccessToken`, and
`WithTokenGetter`. See the complete [token exchange example](examples/token-exchange/main.go).
Callers own credential storage and token persistence. Do not log access tokens,
refresh tokens, ticket URLs, or raw recordings. A static access token does not
provide automatic refresh; use the existing refresh API or a token getter to
supply renewed credentials.

## Connections and device sessions

`Client.OpenSignaling` opens an authenticated WebSocket. Its
`SignalingConnection.StartDeviceSession` creates a child device session from a
caller-provided SDP offer. `DeviceSession` owns negotiated identity, heartbeat,
SDP/ICE exchange, microphone/stream controls, and PTZ requests. This connection
is broader than an RTC media stream; the caller's WebRTC stack transports media.

Session methods include `Answer`, `SendICE`, `PanStep`, `TiltStep`,
`PanContinuous`, `TiltContinuous`, `StopPTZ`, `SetMicrophone`, `SetStreamOptions`,
`Receive`, `Wait`, and `Close`. PTZ results acknowledge commands; they do not
prove physical positioning. Device capabilities vary. Zoom is not verified.

Close the session when finished, close the connection to release its children,
and close the client when its work is done. Keep the connection and session
contexts alive for their intended lifetime. Cancellation ends owned work.
Sessions have a maximum lifetime of 60 minutes, an SDK policy rather than a
proven vendor timeout. See [session design](docs/session-design.md) for SDP
construction, identity domains, heartbeat and teardown rules; sections marked
as target behavior remain implementation requirements.

| Surface | Evidence and limits |
|---|---|
| Authentication and existing HTTP methods | Existing Go regression tests; Python behavior baseline. The new capture contains no OAuth token exchange. |
| v3 device inventory | Recorded Go system tests and Python device-model comparison; Python's legacy inventory route differs. |
| SDP and PTZ | Captured conversation/schema replay plus local connection tests. Signaling ticket bootstrap is separately based on the existing POST mechanism. |
| Other captured HTTP routes | Committed schemas and exchanges; presence in OpenAPI does not imply a public SDK method. |
| Push and playback | Present in recordings; full public abstractions remain planned. Existing event WebSocket behavior is experimental. |

The captured GET `/api/v1/clap/tickets` has not been proven equivalent to the
existing POST signaling ticket bootstrap. Offline replay is not a live
compatibility guarantee for every model, region, or account.

## Configuration and errors

Use `WithHTTPClient` for a custom HTTP client, `WithWebSocketDialer` for signaling
transport, `WithRegion` for US/EU/FE, and `WithEndpoints` for per-client endpoint
overrides. Explicit endpoints win regardless of region option order. EU/FE
Solutions bootstrap defaults are unverified and require an explicit endpoint;
no regional hostname is guessed. Local HTTP/WebSocket servers are supported for
tests. Endpoint constants live in `internal/protocol/endpoints.go`, configuration
in `pkg/ring/endpoints.go`; see [ownership and configuration](docs/constants-and-configuration.md).

Pass bounded contexts to HTTP operations. Check returned errors before using
results, and handle session termination through `Wait` or `Receive`. A lost
connection ends its device sessions; create a new connection explicitly.
Mutating requests are not automatically retried, because a lost reply does not
prove the operation was not executed. Reconcile device state before deciding
whether to retry a timed-out mutation. Automatic HTTP retries are limited to
GET and HEAD, at most three attempts, and are bounded by the request context.

The client does not automatically refresh credentials or repeat an API request
after a 401. A caller may use `RefreshToken`, persist the rotated token response,
and then retry according to its own policy. A 403 indicates an authorization or
permission failure; it does not mean a device is missing. Do not switch to a
different endpoint generation just because an operation returned an error.
The experimental event connection may lose events across a disconnect because
recovery semantics are not verified. Unknown device capabilities should remain
unknown, not be treated as unsupported.

## Protocols and development

- [Architecture and ownership](docs/architecture.md)
- [Overall implementation plan](docs/library-improvement-plan.md) and [porting process](docs/internal/process-of-reverse-engineering.md)
- [OpenAPI HTTP contracts](api/openapi.yaml), [OpenAPI public model projections](api/client-models.openapi.yaml), and [AsyncAPI signaling/JSON-RPC contracts](api/asyncapi.yaml)
- [Complete client interface](pkg/ring/interface.go), [generated HTTP client](pkg/generatedhttp/client.gen.go), [generated public models](pkg/ringapimodels/models.gen.go), and [generated signaling models](pkg/generatedsignaling): run `make generate-api` after editing the schemas. This invokes pinned `oapi-codegen` and Modelina CLIs directly (Go 1.25+ for generation, Node 22; consumers still build with Go 1.24). The HTTP client handles the signaling-ticket request; generated public models describe normalized device, auth, event, and recording results; generated signaling bodies cover live view, ICE, push, playback, and keepalive. `Client`, `SignalingConnection`, `DeviceSession`, `PlaybackSession`, and `PushSubscription` own behavior and lifecycle. Generated code is excluded from the maintained-code coverage budget and checked through recorded replay tests.
- [Recording formats and verification order](docs/replay-format.md)
- [Python fixture replay and original-test migration index](docs/python-replay-harness.md)
- [Remaining migration scope](docs/migration-scope.md)
- [Sanitized recordings](tests/replay/fixtures/recordings/README.md) and [legacy fixture provenance](tests/replay/fixtures/legacy/README.md)
- [Contributing](CONTRIBUTING.md)

Tests have one layout: `tests/replay` exercises the public API against local
HTTP/WebSocket peers driven by checked-in captured or explicitly synthetic
fixtures; `tests/integration` contains opt-in tests against real endpoints.
Small white-box unit tests live beside their implementation in `pkg` or
`internal`. Replay cases isolate session establishment, ICE, PTZ replies,
heartbeat, and termination; the full captured conversation remains an ordering
regression test.

```sh
go test -race ./... -timeout 120s
go vet ./...
go build ./examples/...
make lint
```

Set `GOWORK=off` when testing this module independently of a surrounding workspace.
For the reference-first comparison checks, initialize the submodule, install uv and Node.js/npm,
and run `python tools/verify_reference.py`. This runs the pinned Python tests,
the shared fixture replay with its 95% selected-code coverage gate, and
schema/sanitizer checks in separate local
environments. The private mitmproxy file is not required for CI.

The maintained library coverage gate requires 90% statement coverage, alongside
behavioral and race tests. The completed offline verification run passed at
91.10%; see [verification results](docs/verification.md) for scope and commands. Live tests are opt-in via
`make test-integration` and require explicit credentials and device configuration.

## Compatibility and license

Intentional API changes are allowed during this improvement work. Existing
`StartRTCStream`/`StopRTCStream` were removed. Open one signaling connection and create a `DeviceSession` for live view, ICE, PTZ, and controls. The same connection can create a `PlaybackSession` or `PushSubscription`. See [migration guidance](docs/migration.md) for changed lifecycle, retry, and
error behavior.

The [device-session example](examples/device-session/main.go) accepts `-ptz-demo`:
it sends `PanContinuous` to the right at speed `0.5`, waits one second, then
sends `StopPTZ` for the pan axis, even if interrupted. Continuous movement
must always be stopped explicitly; a successful RPC means the command was
acknowledged, not that the camera reached a particular position.

Control calls now follow the replayed legacy request profiles. `SetVolume`
requires `Kind` (`"chime"` or `"doorbell"`) and the device `Description`;
`SetInHomeChime` requires `Description` and exactly one of `type`, `enabled`,
or `duration` in `Settings`. `SetLights` accepts on/off without a duration;
`SetMotionDetection` uses the captured settings PATCH. These calls replaced
unverified generic device routes; see [HTTP request shapes](docs/protocols/http.md).

Go implementation: Apache-2.0, see [LICENSE](LICENSE). The separately vendored
Python reference retains its own LGPL-3.0-or-later license. Its source and tests
are a comparison baseline, not a relicensing of this library.
