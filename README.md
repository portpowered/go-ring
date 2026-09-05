# go-ring

A standalone Go client for Ring authentication, devices, controls, recording history, downloads, and RTC signaling. Requires Go 1.24 or newer. The client exposes vendor models and does not require a hosted service.

## Install

```bash
go get github.com/portpowered/go-ring@v0.1.0
```

## List devices

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "github.com/portpowered/go-ring/pkg/ring"
)

func main() {
    client, err := ring.NewClient(ring.WithAccessToken(os.Getenv("RING_ACCESS_TOKEN")))
    if err != nil { log.Fatal(err) }
    defer client.Close()
    devices, err := client.ListDevices(context.Background())
    if err != nil { log.Fatal(err) }
    fmt.Println(len(devices.Doorbells))
}
```

Use `Authenticate(ctx, ring.AuthenticateRequest{...})` for username/password and optional OTP, or `RefreshToken(ctx, ring.RefreshTokenRequest{...})` to refresh a session. `WithTokenGetter` lets a caller supply credentials dynamically. Callers own secure storage and account permissions. Inspect typed errors in `pkg/ringapimodels` to distinguish authentication, HTTP, network, and closed-connection failures.

## Supported surface and evidence

| Surface | Automated evidence | Limits |
| --- | --- | --- |
| Authentication, devices, controls, history and recordings | Injected HTTP transport with sanitized response fixtures and error cases | No live account validation is claimed by CI |
| RTC signaling | Local WebSocket handshake, SDP and ICE tests | Customer supplies WebRTC media handling; see [RTC notes](docs/rtcstream.md) |
| Generic event WebSocket | Local message, cancellation, callback and shutdown tests | Default vendor event URL is experimental and has not been verified; configure `WithEventWebSocketURL` for a verified endpoint |

Close each event connection and RTC stream explicitly. Client `Close` marks the client closed; it does not own returned streams. Cancellation interrupts idle event reads. Vendor APIs may change independently of this module.

## Examples

Examples use real credentials/devices and are never run by normal CI:

- `go run ./examples/token-exchange` — authentication and refresh; deliberately prints issued credentials for interactive use, so do not capture output in shared logs.
- `go run ./examples/enumerate-devices` — list devices.
- `go run ./examples/chime-sound` — trigger a device action.
- `go run ./examples/download-recordings` — download a recording.
- `go run ./examples/rtc-stream` — RTC signaling.

Read the environment-variable checks in each example before running it. `make build-examples` compiles all examples without executing them.

## Development

```bash
make check
make test-race
make build-examples
```

Normal tests use fixtures and local servers, with no vendor credentials. Live tests require `make test-integration` and explicit environment configuration. See [fixture provenance](test/fixtures/README.md) and [contributing](CONTRIBUTING.md).

## Releases and compatibility

Maintainers run CI on supported Go versions and publish immutable semantic-version tags. Before v1, minor releases may change APIs; release notes identify compatibility changes. Consumers should pin versions. Source modules are the release artifact; examples are not distributed application binaries.

Licensed under Apache-2.0; see [LICENSE](LICENSE).
