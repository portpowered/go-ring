# go-ring

[![CI](https://github.com/portpowered/go-ring/actions/workflows/ci.yml/badge.svg)](https://github.com/portpowered/go-ring/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/portpowered/go-ring.svg)](https://pkg.go.dev/github.com/portpowered/go-ring)
[![License](https://img.shields.io/github/license/portpowered/go-ring)](LICENSE)

A Go client for Ring authentication, devices, controls, recordings, and persistent
signaling sessions.

Supports certain functionality that other libraries don't. Mainly:
1. PTZ support during an active video stream
2. Updates to support new 2FA variants/Oauth changes.
3. Updates to newer API endpoints v3/devices
4. Missing functionality like device reboot that was missing in other libraries.

## Install

Requires Go 1.24 or later.

```sh
go get github.com/portpowered/go-ring
```

# Features

See the [feature matrix](docs/developer-facing/parity-matrix.md) for details.

0. auth token exchange, and auth setup
1. enumeration of devices
2. video stream setup via webRTC. PTZ/mic/etc controls during active session
3. various device commands such as play sound, reboot, alarm test, and other settings
4. session event stream notifying of system changes
5. various others

# Examples

see [the examples dirctory](./examples/) for more examples.

## Auth token retrieval

1. request a 2 factory auth (2FA) code for your username/password.
2. get the 2FA code from your 2FA device
3. login again, with the 2FA code and the corresponding username/password

```go
func main() {
    username := "X"
    password := "X"

    // Request a code for OTP
    client, _ := ring.NewClient()
    client.Request2FACode(ctx, ring.Request2FACodeRequest{
        Username: username,
        Password: password,
    })

    // You'll then get some OTP code on your MFA device (phone or whatever else), and you should pass it in.
    scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan() // waits until the user presses Enter
	optCode := scanner.Text()

    // Authenticate with said OTP
    tokens, _ := client.Authenticate(ctx, ring.AuthenticateRequest{
        Username: username,
        Password: password,
        OTPCode:  otpCode,
    })
}
```
## List devices

1. using the tokens you generated for an account, enumerate the devices.

```go

func main() {
    client, _ := ring.NewClient(ring.WithAccessToken("YOUR_ACCESS_TOKEN"))

    devices, _ := client.ListDevices(context.Background())
    fmt.Println(len(devices.Doorbells))
}
```

# References


## User resources

## Library Developer resources
### Architecture
- [Architecture and ownership](docs/developer-facing/library-architecture.md)

### Library systems
- [OpenAPI Library structs](api/client-models.openapi.yaml)
- [Recording formats and verification order](docs/developer-facing/replay-format.md)
- [Replay, unit, and live integration coverage](docs/developer-facing/coverage.md)
- [Generated dependency models](api/dependency-models.openapi.yaml)


### Reverse engineering

- [Overall description of ring API system](docs/developer-facing/ring-api-architecture.md)
- [Reverse engineering process](docs/internal/process-of-reverse-engineering.md)
- [OpenAPI HTTP contracts](api/openapi.yaml), and [AsyncAPI signaling/JSON-RPC contracts](api/asyncapi.yaml)
- [Replay/recordings for APIs](tests/replay/fixtures/recordings/README.md)

### contribution
- [Contributing](CONTRIBUTING.md)


## Compatibility and license
Go implementation: Apache-2.0, see [LICENSE](LICENSE).

The separately vendored
Python reference retains its own LGPL-3.0-or-later license. Its source and tests
are a comparison baseline, not a relicensing of this library.
