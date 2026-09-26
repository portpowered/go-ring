# go-ring

[![CI](https://github.com/portpowered/go-ring/actions/workflows/ci.yml/badge.svg)](https://github.com/portpowered/go-ring/actions/workflows/ci.yml)
[![Replay coverage](https://github.com/portpowered/go-ring/wiki/coverage.svg)](https://raw.githack.com/wiki/portpowered/go-ring/coverage.html)
[![Go Reference](https://pkg.go.dev/badge/github.com/portpowered/go-ring.svg)](https://pkg.go.dev/github.com/portpowered/go-ring)
[![License](https://img.shields.io/github/license/portpowered/go-ring)](LICENSE)

A Go client for Ring authentication, device discovery and controls, recordings,
and persistent signaling sessions.

## Install

Requires Go 1.24 or later.

```sh
go get github.com/portpowered/go-ring
```

## Examples
We go through auth, enumeration, reboot, webRTC, and then finally pan/tilt in an active webRTC session.

### 1. Authenticate

Create an isolated login session so the 2FA challenge, cookies, and hardware ID
stay with one exchange.


```go
client, _ := ring.NewClient()
defer client.Close()
login, err := client.NewLoginSession(ring.LoginSessionRequest{Username: username, Password: password})
if err != nil { return err }
defer login.Close()

// Request a OTP code for the user.
if err := login.Request2FACode(ctx); err != nil { return err }


// Here you'll want to take in a buffer and get the otp code somehow
otpCode := "123123"

// Read otpCode from the user's 2FA channel before continuing.
tokens, err := login.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otpCode})
if err != nil { return err }
accessToken := tokens.AccessToken
auth := ring.AuthContext{AccessToken: accessToken, HardwareID: login.HardwareID()}
```

[access token example](examples/token-exchange/main.go).
### 2. List devices


```go
client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()

devices, err := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: ring.AuthContext{AccessToken: accessToken}})
if err != nil { return err }
for _, doorbell := range devices.Doorbells {
    fmt.Printf("%s: %s\n", doorbell.ID, doorbell.Name)
}
for _, chime := range devices.Chimes {
    fmt.Printf("%s: %s\n", chime.ID, chime.Name)
}
for _, camera := range devices.StickUpCams {
    fmt.Printf("%s: %s\n", camera.ID, camera.Name)
}
for _, other := range devices.Other {
    fmt.Printf("%s: %s\n", other.ID, other.Name)
}
```

[enumerate-devices example](examples/enumerate-devices/main.go).

### 3. Reboot a device

```go
client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()

if err := client.RebootDevice(ctx, ring.DeviceIDRequest{Auth: ring.AuthContext{AccessToken: accessToken}, DeviceID: deviceID}); err != nil {
    return err
}
```

[reboot-device example](examples/reboot-device/main.go).

### 4. Play a chime test sound

```go
client, err := ring.NewClient()
if err != nil { return err }
defer client.Close()

if err := client.TestSound(ctx, ring.TestSoundRequest{
    Auth:     ring.AuthContext{AccessToken: accessToken},
    DeviceID: chimeID,
    Sound:    ringapimodels.SoundKindDing,
}); err != nil {
    return err
}
```

[chime-sound example](examples/chime-sound/main.go)

### 5. Establish a live WebRTC session

For a live feed, you do the following:
1. create a local webRTC offer
2. start a device session with said offer
3. apply the answer for the offer to the webRTC session on your peer.

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
conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: ring.AuthContext{AccessToken: accessToken}})
if err != nil { return err }
defer conn.Close()

// establish device session
session, err := conn.StartDeviceSession(ctx, ring.StartDeviceSessionRequest{
    DeviceID:     deviceID,
    Offer:        ring.SessionDescription{Type: ring.SDPTypeOffer, SDP: pc.LocalDescription().SDP},
    VideoEnabled: true,
    ICEMode:      ring.ICENonTrickle,
})

// set auth description.
if err != nil { return err }
defer session.Close()
if err := pc.SetRemoteDescription(webrtc.SessionDescription{
    Type: webrtc.SDPTypeAnswer,
    SDP:  session.Answer().SDP,
}); err != nil { return err }
```


then apply the answer to the same peer. This uses non-trickle ICE; the full
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

For `TiltContinuous`, stop with `ring.TiltAxis`;
[pan, tilt, zoom examples](examples/rtc_ptz/rtc_ptz.go)


## More examples

More examples below

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

## Supported operations

These are the public SDK methods.
1. `Client` handles account and HTTP operations.
2. After you create a client you can create a persistent websocket connection with `SignalingConnection`, which is a stateful network connection.
3. From that stateful network connection, you can establish device sessions for live viewing video camera feeds, and what not.

| Feature | Object and Go calls | Notes |
| --- | --- | --- |
| Client setup and shutdown | `ring.NewClient`, `Client.Close` | Configuration is fixed at construction; one client can own several signaling connections. |
| Login and tokens | `Client.NewLoginSession`, `LoginSession.Request2FACode`, `LoginSession.Authenticate`, `Client.RefreshToken` | Login state stays in one session; refresh tokens are passed per request. See [token exchange](examples/token-exchange/main.go). |
| Device inventory and lookup | `Client.ListDevices`, `Client.GetDevice`, `Client.GetDeviceDetail` | `GetDeviceDetail` returns a typed client projection of the captured v3 response. |
| Device health and settings | `Client.UpdateDeviceHealth`, `Client.GetDeviceSettings`, `Client.PatchDeviceSettings` | Health uses the generic device route and needs only a device ID. |
| Locations and groups | `Client.ListLocations`, `Client.GetLocation`, `Client.ListLocationGroups`, `Client.ListLocationDevices` | Requests and results use client-owned types; the generated HTTP models stay inside the transport. |
| Motion and device controls | `Client.SetMotionDetection`, `Client.SetLights`, `Client.SetSiren`, `Client.SetVolume`, `Client.SetInHomeChime` | Volume needs a device ID, `VolumeKind`, and current description; in-home chime needs a device ID and current description. These values are sent to Python-compatible legacy routes without an inventory lookup. |
| Chime sound and reboot | `Client.TestSound`, `Client.RebootDevice` | See the inline code below. |
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
| Push notifications | `SignalingConnection.SubscribePush`, `PushSubscription.Receive`, `PushSubscription.Close` | Requires an active signaling connection. |
| Captured GET ticket | `Client.GetCapturedTickets` | Separate from the POST ticket used by `OpenSignaling`. |

Captured reads use SDK request and result types. For example,
`GetLocationRequest.Params.Include` accepts `[]ring.LocationExpansion`, and
`GetDeviceTimelineRequest.Params.Order` accepts `ring.TimelineOrder`.
`EventCapability`, `HistoryKind`, and captured event/status types have named
values but remain open to future server values. The generated HTTP types are
used only inside the transport.
Callers upgrading from wire-model results should use `DeviceDetail.Device.ID`
and `LocationList.Locations`, and replace `generatedhttp.Get*Params` fields in
client requests with `ring.LocationParams`, `ring.TimelineParams`,
`ring.HistoryDevicesParams`, or `ring.CapturedTicketsParams`.

## References

- [Architecture and ownership](docs/developer-facing/library-architecture.md)
- [OpenAPI HTTP contracts](api/openapi.yaml) and [AsyncAPI signaling/JSON-RPC contracts](api/asyncapi.yaml)
- [Public model schema](api/client-models.openapi.yaml)
- [Recording formats and verification order](docs/developer-facing/replay-format.md)
- [Replay, unit, and live integration coverage](docs/developer-facing/coverage.md)
- [Ring API architecture](docs/developer-facing/ring-api-architecture.md)
- [Reverse engineering process](docs/internal/process-of-reverse-engineering.md)
- [API replay recordings](tests/replay/fixtures/recordings/README.md)
- [Contributing](CONTRIBUTING.md)

## Compatibility and license
Go implementation: Apache-2.0, see [LICENSE](LICENSE).

The upstream [python-ring-doorbell](https://github.com/python-ring-doorbell/python-ring-doorbell)
project remains a comparison baseline. It is not included in this repository.
