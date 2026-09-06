# go-ring

[![CI](https://github.com/portpowered/go-ring/actions/workflows/ci.yml/badge.svg)](https://github.com/portpowered/go-ring/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/portpowered/go-ring)](https://github.com/portpowered/go-ring/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/portpowered/go-ring)](https://go.dev/)
[![Go Reference](https://pkg.go.dev/badge/github.com/portpowered/go-ring.svg)](https://pkg.go.dev/github.com/portpowered/go-ring)
[![License](https://img.shields.io/github/license/portpowered/go-ring)](LICENSE)
![GitHub stars](https://img.shields.io/github/stars/portpowered/go-ring?style=social)

Golang library for integrating against ring doorbells. 

# Install

```bash
go get github.com/portpowered/go-ring@v0.1.0
```


# Examples
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

## Auth

Use `Authenticate(ctx, ring.AuthenticateRequest{...})` for username/password and optional OTP, or `RefreshToken(ctx, ring.RefreshTokenRequest{...})` to refresh a session. `WithTokenGetter` lets a caller supply credentials dynamically. Callers own secure storage and account permissions.

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
    client, err := ring.NewClient()
    err = client.Request2FACode(ctx, ring.Request2FACodeRequest{
        Username: username,
        Password: password,
    })

    authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{
		Username: username,
		Password: password,
		OTPCode:  otpCode,
	})
}

```

From that authResp, the object looks like
```

// AuthResponse represents an authentication response
type AuthResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	TokenType    string
}
```

You then use the access token to access websites, and store them for as long as the token refresh duration lasts. 

# Supported surface and evidence

1. Authentication to retrieve auth tokens. Via refresh token | OTP
2. Video connection via WebRTC
3. device controls for volume

# Examples

Examples use real credentials/devices and are never run by normal CI:

- `go run ./examples/token-exchange` — authentication and refresh.
- `go run ./examples/enumerate-devices` — list devices.
- `go run ./examples/chime-sound` — trigger a device action.
- `go run ./examples/download-recordings` — download a recording.
- `go run ./examples/rtc-stream` — RTC signaling.


# Development

```bash
make check
make test-race
make build-examples
```

Normal tests use fixtures and local servers, with no vendor credentials. Live tests require `make test-integration` and explicit environment configuration. See [fixture provenance](test/fixtures/README.md) and [contributing](CONTRIBUTING.md).

## Architecture definition

How ring works is basically you setup an auth connection and exchange some tokens for auth. 
You use those auth tokens to establish a websocket connection. 
The websocket connection is used as a data channel to like make sure a connection is alive, and to send/receive messages. 
Additioanlly, the websocket is used to send a signal to establish a video connection, via sending a webRTC SDP. 
The SDP is then sent to establish a connection between the device and some webRTC target. 

## References

Implementation was based on various libraries: 
### Python
1. https://github.com/python-ring-doorbell/python-ring-doorbell
### PHP
2. https://github.com/jeroenmoors/php-ring-api
### Javascript
3. https://github.com/tsightler/ring-mqtt
4. https://github.com/dgreif/ring

## Releases and compatibility

Licensed under Apache-2.0; see [LICENSE](LICENSE).
