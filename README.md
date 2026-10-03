# go-ring

[![Go version](https://img.shields.io/github/go-mod/go-version/portpowered/go-ring)](go.mod)
[![CI](https://github.com/portpowered/go-ring/actions/workflows/ci.yml/badge.svg)](https://github.com/portpowered/go-ring/actions/workflows/ci.yml)
[![Replay coverage](https://github.com/portpowered/go-ring/wiki/coverage.svg)](https://raw.githack.com/wiki/portpowered/go-ring/coverage.html)
[![Release](https://img.shields.io/github/v/release/portpowered/go-ring?display_name=tag)](https://github.com/portpowered/go-ring/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/portpowered/go-ring.svg)](https://pkg.go.dev/github.com/portpowered/go-ring)
[![License](https://img.shields.io/github/license/portpowered/go-ring)](LICENSE)
[![Documentation](https://img.shields.io/badge/docs-GitHub%20Pages-blue)](https://portpowered.github.io/go-ring/)

A Go client for Ring authentication, device discovery and controls, recordings,
notifications, and live sessions. Ring behavior can vary by account, device,
and region.

Requires Go 1.24 or later.

```sh
go get github.com/portpowered/go-ring@latest
```

## Authenticate

Create a login session, request a verification code, and complete the exchange.
The caller supplies the current access token with each account request.

```go
client, err := ring.NewClient()
if err != nil {
    return err
}
login, err := client.NewLoginSession(ring.LoginSessionRequest{
    Username: username,
    Password: password,
})
if err != nil {
    return err
}
defer login.Close()

if err := login.Request2FACode(ctx); err != nil {
    return err
}
tokens, err := login.Authenticate(ctx, ring.CompleteLoginRequest{
    OTPCode: os.Getenv("RING_OTP_CODE"),
})
if err != nil {
    return err
}
auth := ring.AuthContext{
    AccessToken: tokens.AccessToken,
    HardwareID:  login.HardwareID(),
}
```

See the [authentication guide](https://portpowered.github.io/go-ring/docs/guides/authentication/)
for token refresh and secure token storage.

## List devices

Use the same authentication context for device requests.

```go
devices, err := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: auth})
if err != nil {
    return err
}
for _, device := range devices.Devices {
    fmt.Printf("%s: %s (%s)\n", device.ID, device.Name, device.Kind)
}
```

The [device operations guide](https://portpowered.github.io/go-ring/docs/guides/device-operations/)
covers controls, capabilities, and device-specific behavior.

## Open a live session

Create a WebRTC offer with your peer connection, then pass it to the client.
The returned answer belongs to that same peer. Close the session and signaling
connection when finished.

```go
conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
if err != nil {
    return err
}
defer conn.Close()

session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
    DeviceID:     deviceID,
    Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: offer.SDP},
    VideoEnabled: true,
    ICEMode:      ring.ICENonTrickle,
})
if err != nil {
    return err
}
defer session.Close()

answer := session.Answer()
if err := peer.SetRemoteDescription(webrtc.SessionDescription{
    Type: webrtc.SDPTypeAnswer,
    SDP:  answer.SDP,
}); err != nil {
    return err
}
```

See the [live sessions guide](https://portpowered.github.io/go-ring/docs/guides/live-sessions/)
for complete WebRTC and ICE setup, and the [PTZ example](examples/rtc_ptz/rtc_ptz.go)
for camera movement and stop commands.

## More examples

| Workflow | Runnable example |
| --- | --- |
| Authenticate and save tokens | [token-exchange](examples/token-exchange/main.go) |
| List devices | [enumerate-devices](examples/enumerate-devices/main.go) |
| Reboot a device | [reboot-device](examples/reboot-device/main.go) |
| Play a chime test sound | [chime-sound](examples/chime-sound/main.go) |
| Stream live video | [rtc_stream](examples/rtc_stream/rtc_stream.go) |
| Control PTZ | [rtc_ptz](examples/rtc_ptz/rtc_ptz.go) |
| Receive push events | [session_push_events](examples/session_push_events/session_push_events.go) |
| Download recordings | [download-recordings](examples/download-recordings/main.go) |

## Documentation

- [Customer guides](https://portpowered.github.io/go-ring/docs/guides/) cover authentication, configuration, devices, recordings, notifications, live sessions, and playback.
- [Generated API reference](https://portpowered.github.io/go-ring/) documents the client and its request and result types.
- [Diagnostic CLI guide](https://portpowered.github.io/go-ring/docs/guides/diagnostic-cli/) introduces the optional `go-ring` command-line tool.

## Issues and license

Please [open an issue](https://github.com/portpowered/go-ring/issues) for a
reproducible bug or unsupported device behavior. go-ring is licensed under
Apache-2.0; see [LICENSE](LICENSE).
