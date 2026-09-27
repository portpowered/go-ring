# go-ring

[![CI](https://github.com/portpowered/go-ring/actions/workflows/ci.yml/badge.svg)](https://github.com/portpowered/go-ring/actions/workflows/ci.yml)
[![Replay coverage](https://github.com/portpowered/go-ring/wiki/coverage.svg)](https://raw.githack.com/wiki/portpowered/go-ring/coverage.html)
[![Release](https://img.shields.io/github/v/release/portpowered/go-ring?display_name=tag)](https://github.com/portpowered/go-ring/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/portpowered/go-ring.svg)](https://pkg.go.dev/github.com/portpowered/go-ring)
[![License](https://img.shields.io/github/license/portpowered/go-ring)](LICENSE)

A Go client for Ring authentication, device discovery and controls, recordings,
and persistent signaling sessions.

## API reference

The go-ring API reference will be published at
[portpowered.github.io/go-ring](https://portpowered.github.io/go-ring/) after
the Pages workflow runs. It is generated from the supported Ring HTTP and
signaling contracts in
[`api/openapi.yaml`](api/openapi.yaml) and [`api/asyncapi.yaml`](api/asyncapi.yaml).
The reference documents third-party APIs observed and modeled by this project;
it does not describe a public API offered by go-ring itself. The GitHub Pages
workflow rebuilds it when either contract changes.

This is a bit unique compared to other ring-doorbell libraries since: 
1. It supports PTZ over signaling WebSockets and newer API versions.
2. Its replay suite covers sanitized 2026 network captures.

## Install

Requires Go 1.24 or later.

```sh
go get github.com/portpowered/go-ring@v0.4.0
```

## Examples

The examples below cover authentication, device discovery, controls, and a
live WebRTC session.

### 1. Authenticate

Create a login session, request a verification code, and complete the exchange.

```go
client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()
login, err := client.NewLoginSession(ring.LoginSessionRequest{Username: username, Password: password})
if err != nil { return err }
defer login.Close()

// Request a verification code for the user.
if err := login.Request2FACode(ctx); err != nil { return err }

// Read the code from the user's verification channel.
otpCode := os.Getenv("RING_OTP_CODE")
tokens, err := login.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otpCode})
if err != nil { return err }
accessToken := tokens.AccessToken
auth := ring.AuthContext{AccessToken: accessToken, HardwareID: login.HardwareID()}
```

[Token exchange example](examples/token-exchange/main.go).

### 2. List devices

Pass the resulting authentication context to each request.

```go
client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()

devices, err := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: auth})
if err != nil { return err }
for _, device := range devices.Devices {
    fmt.Printf("%s: %s (%s)\n", device.ID, device.Name, device.Kind)
    if device.Supports(ringapimodels.DeviceCapabilityPtzPanStep) {
        fmt.Println("  supports step pan control")
    }
}
```

Each device reports capabilities confirmed by its inventory response. An absent
capability means support is unknown, so the server remains the final authority
for a control request. See the [device enumeration plan](docs/plans/device-enumeration.md).

[enumerate-devices example](examples/enumerate-devices/main.go).

### 3. Reboot a device

```go
client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()

if err := client.RebootDevice(ctx, ring.DeviceIDRequest{Auth: auth, DeviceID: deviceID}); err != nil {
    return err
}
```

[reboot-device example](examples/reboot-device/main.go).

### 4. Play a chime test sound

This command requires a chime device.

```go
client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()

if err := client.TestSound(ctx, ring.TestSoundRequest{
    Auth:     auth,
    DeviceID: chimeID,
    Sound:    ringapimodels.SoundKindDing,
}); err != nil {
    return err
}
```

[Chime sound example](examples/chime-sound/main.go).

### 5. Establish a live WebRTC session

Create a local WebRTC offer, start a device session with it, then apply the
answer to the same peer connection.

```go
// create new peer connection
pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
if err != nil { return err }
defer pc.Close()
if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
    webrtc.RtpTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
    return err
}
pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
    for {
        if _, _, err := track.ReadRTP(); err != nil { return }
    }
})
offer, err := pc.CreateOffer(nil)
if err != nil { return err }
gathered := webrtc.GatheringCompletePromise(pc)
if err := pc.SetLocalDescription(offer); err != nil { return err }
select {
case <-gathered:
case <-ctx.Done(): return ctx.Err()
}


client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()

// establish persistent connection session
conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
if err != nil { return err }
defer conn.Close()

// establish device session
session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
    DeviceID:     deviceID,
    Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: pc.LocalDescription().SDP},
    VideoEnabled: true,
    ICEMode:      ring.ICENonTrickle,
})

if err != nil { return err }
defer session.Close()
if err := pc.SetRemoteDescription(webrtc.SessionDescription{
    Type: webrtc.SDPTypeAnswer,
    SDP:  session.Answer().SDP,
}); err != nil { return err }
```


This uses non-trickle ICE; the full
[rtc_stream example](examples/rtc_stream/rtc_stream.go) also shows trickle ICE,
remote candidates, and session events. The library handles signaling, while
your application owns the Pion peer and media rendering.

### 6. Move the camera and stop

Once `StartDeviceSession` returns you can move the camera around if your camera supports it.

```go
// move around
_, err := session.PanContinuous(ctx, ring.PanContinuousRequest{
    Direction: ring.PanRight,
    Speed:     0.5, // normalized range: 0 to 1
})
if err != nil { return err }
select {
case <-time.After(time.Second):
case <-ctx.Done():
}
stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
// stop the move.
_, err = session.StopPTZ(stopCtx, ring.StopPTZRequest{Axis: ring.PanAxis})
return err
```

For `TiltContinuous`, stop with `ring.TiltAxis`. See the
[pan and tilt example](examples/rtc_ptz/rtc_ptz.go).


## More examples

| Task | Runnable source | Run | Additional input |
| --- | --- | --- | --- |
| Authenticate and save tokens | [token-exchange](examples/token-exchange/main.go) | `go run ./examples/token-exchange` | `RING_USERNAME`, `RING_PASSWORD`; optional `RING_OTP_CODE` |
| List devices and IDs | [enumerate-devices](examples/enumerate-devices/main.go) | `go run ./examples/enumerate-devices` | None |
| Reboot one device | [reboot-device](examples/reboot-device/main.go) | `go run ./examples/reboot-device` | `RING_DEVICE_ID` |
| Play a chime test sound | [chime-sound](examples/chime-sound/main.go) | `go run ./examples/chime-sound` | `RING_CHIME_ID` |
| Establish a WebRTC live session | [rtc_stream](examples/rtc_stream/rtc_stream.go) | `go run ./examples/rtc_stream` | `RING_DEVICE_ID`; optional `RING_ICE_SERVERS_JSON` |
| Pan after session establishment, then stop | [rtc_ptz](examples/rtc_ptz/rtc_ptz.go) | `go run ./examples/rtc_ptz` | `RING_DEVICE_ID`; optional `RING_ICE_SERVERS_JSON` |
| Receive push events | [session_push_events](examples/session_push_events/session_push_events.go) | `go run ./examples/session_push_events` | `RING_DEVICE_ID` |
| Download recordings | [download-recordings](examples/download-recordings/main.go) | `go run ./examples/download-recordings` | See example source |

## CLI quick start

Build the diagnostic CLI from its separate Go module. It uses the public
library, saves login tokens locally, and needs FFmpeg for snapshots and the
default live preview.

```sh
git clone https://github.com/portpowered/go-ring.git
cd go-ring/cmd/go-ring
go build -o go-ring .
./go-ring auth login
./go-ring devices list
./go-ring siren <device-id> on
./go-ring snapshot <device-id> --output camera.jpg
./go-ring view <device-id> --continuous
./go-ring events watch <device-id> --duration 60s
```

Use the arrow keys to pan or tilt during `view`, Space to stop continuous
movement, and `q` to leave. On Windows, run `go build -o go-ring.exe .` and
`./go-ring.exe ...`. To investigate decoder warnings, add
`--record-rtp camera.rtp` to `view`, then use
`./go-ring replay-video camera.rtp --output camera.h264` for offline playback.
The recording contains camera footage. `events watch` registers an account-scoped
FCM receiver, subscribes to camera motion, and stores its credentials privately
for reconnection. Registration and connection do not guarantee that a motion
notification will occur during the watch window. See the [CLI guide](cmd/go-ring/README.md)
for health, reboot, chime sound, and capture details.

For a compact device status, pass the same authentication context and device ID:

```go
status, err := client.GetDeviceStatus(ctx, ring.GetDeviceDetailRequest{Auth: auth, DeviceID: deviceID})
if err != nil { return err }
fmt.Printf("offline: %t\n", status.IsOffline)
if status.BatteryPercent != nil {
    fmt.Printf("battery: %.0f%%\n", *status.BatteryPercent)
}
```

An intercom can be unlocked by ID. A successful call means Ring accepted the
command; it does not establish that the door physically opened.

```go
err := client.UnlockIntercom(ctx, ring.DeviceIDRequest{Auth: auth, DeviceID: intercomID})
if err != nil { return err }
```

To receive ding or motion events, keep one FCM connection open for the account.
Persist each credentials event securely and pass it back on reconnect. The
`Client` does not retain credentials or account authorization.

```go
push, err := client.ConnectPush(ctx, ring.ConnectPushRequest{
    Auth: auth, DeviceIDs: []string{deviceID}, Ding: true, Motion: true,
    Credentials: savedFCMCredentials,
})
if err != nil { return err }
defer push.Close()
for event := range push.Events() {
    switch event.Kind {
    case ring.PushCredentials:
        if err := saveSecurely(event.Credentials); err != nil { return err }
    case ring.PushMessage:
        fmt.Printf("device=%s action=%s\n", event.DeviceID, event.Action)
    case ring.PushRetry:
        return event.Err
    }
}
```

The FCM transport was live-tested through registration and connection with a
camera. Ding, motion, and intercom unlock delivery remain source-contract tests
until those events can be triggered and observed on real devices.

## Supported operations

`Client` handles authentication and HTTP requests. `SignalingConnection` owns
the persistent signaling socket; it can start live device sessions, playback
sessions, and push subscriptions.

| Feature | Object and Go calls | Notes |
| --- | --- | --- |
| Client setup and shutdown | `ring.NewClient`, `Client.Close` | Configuration is fixed at construction; one client can own several signaling connections. |
| Login and tokens | `Client.NewLoginSession`, `LoginSession.Request2FACode`, `LoginSession.Authenticate`, `Client.RefreshToken` | Login state stays in one session; refresh tokens are passed per request. See [token exchange](examples/token-exchange/main.go). |
| Device inventory and lookup | `Client.ListDevices`, `Client.GetDevice`, `Client.GetDeviceDetail`, `Device.Supports` | One list of devices; capabilities come from explicit inventory fields. `GetDeviceDetail` returns the typed captured v3 detail. |
| Device status, health, and settings | `Client.GetDeviceStatus`, `DeviceDetailDevice.Status`, `Client.UpdateDeviceHealth`, `Client.GetDeviceSettings`, `Client.PatchDeviceSettings` | Battery is unknown on wired devices unless battery presence is explicit; missing connection state is not treated as offline. |
| Locations and groups | `Client.ListLocations`, `Client.GetLocation`, `Client.ListLocationGroups`, `Client.ListLocationDevices` | Requests and results use client-owned types; the generated HTTP models stay inside the transport. |
| Motion and device controls | `Client.SetMotionDetection`, `Client.SetLights`, `Client.SetSiren`, `Client.SetVolume`, `Client.SetInHomeChime` | Volume and in-home chime updates require the current device description for their legacy routes. |
| Chime sound and reboot | `Client.TestSound`, `Client.RebootDevice` | See the inline code below. |
| Intercom unlock | `Client.UnlockIntercom` | Source-derived request replay; requires live intercom confirmation. |
| Snapshot | `Client.GetSnapshot` | Returns image bytes and metadata. |
| Recording history | `Client.GetDeviceHistory`, `Client.GetHistoryDevices`, `Client.GetDeviceTimeline`, `Client.GetActiveDings`, `Client.GetLastRecordingID` | Legacy history and captured EVM history/timeline are separate APIs. |
| Recording media and changes | `Client.GetRecording`, `Client.GetRecordingShareURL`, `Client.FavoriteRecording`, `Client.DeleteRecording` | Close the body returned by `GetRecording`. |
| Persistent live-view setting | `Client.SetPersistentLiveViewEnabled` | Changes a device setting; it does not start a live session. |
| Account event stream | `Client.ConnectEvents`, `EventConnection.Receive`, `EventConnection.Close`, or `Client.Listen` | Separate from signaling push subscriptions. |
| Signaling connection | `Client.OpenSignaling`, `SignalingConnection.Close`, `SignalingConnection.Err` | One socket can own multiple conversations. |
| Live session setup | `SignalingConnection.StartDeviceSession`, `DeviceSession.Answer`, `DeviceSession.SendICE` | The application creates and owns the WebRTC peer. |
| Live PTZ | `DeviceSession.PanStep`, `DeviceSession.TiltStep`, `DeviceSession.PanContinuous`, `DeviceSession.TiltContinuous`, `DeviceSession.StopPTZ` | Continuous movement should be followed by `StopPTZ`. |
| Live audio and options | `DeviceSession.SetMicrophone`, `DeviceSession.SetStreamOptions` | Commands on an active device session. |
| Live events and lifecycle | `DeviceSession.Receive`, `DeviceSession.State`, `DeviceSession.Wait`, `DeviceSession.Close` | `Wait` reports termination; sessions have a 60-minute maximum. |
| Cloud playback | `SignalingConnection.StartPlayback`, `PlaybackSession.Answer`, `PlaybackSession.SendICE`, `PlaybackSession.Receive`, `PlaybackSession.Close` | Playback negotiates its own SDP/ICE conversation. |
| Signaling push | `SignalingConnection.SubscribePush`, `PushSubscription.Receive`, `PushSubscription.Close` | Requires an active signaling connection; captured replay covers shoulder taps. |
| FCM notifications | `Client.ConnectPush`, `PushConnection.Events`, `PushConnection.Close`, `Client.RegisterPushDevice`, `Client.SubscribeDeviceDing`, `Client.SubscribeDeviceMotion` | Optional long-lived account connection; live registration and connection verified on a camera, event delivery pending a triggered notification. |
| Captured GET ticket | `Client.GetCapturedTickets` | Separate from the POST ticket used by `OpenSignaling`. |
## References

### Reverse engineering resources

- [Ring API architecture](docs/developer-facing/ring-api-architecture.md)
- [Reverse engineering process](docs/internal/process-of-reverse-engineering.md)
- [Ring OpenAPI HTTP contracts](api/openapi.yaml) and [Ring AsyncAPI signaling/JSON-RPC contracts](api/asyncapi.yaml)

### Internal development resources

- [Architecture and ownership](docs/developer-facing/library-architecture.md)
- [Public model schema](api/client-models.openapi.yaml)
- [Recording formats and verification order](docs/developer-facing/replay-format.md)
- [Replay, unit, and live integration coverage](docs/developer-facing/coverage.md)
- [Device enumeration and capabilities](docs/plans/device-enumeration.md)
- [API replay recordings](tests/replay/fixtures/README.md)
- [Contributing](CONTRIBUTING.md)

## Issues

Please [open an issue](https://github.com/portpowered/go-ring/issues) for a
reproducible bug or unsupported device behavior.

## Compatibility and license
Go implementation: Apache-2.0, see [LICENSE](LICENSE).
