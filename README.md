# go-ring

[![CI](https://github.com/portpowered/go-ring/actions/workflows/ci.yml/badge.svg)](https://github.com/portpowered/go-ring/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/portpowered/go-ring.svg)](https://pkg.go.dev/github.com/portpowered/go-ring)
[![License](https://img.shields.io/github/license/portpowered/go-ring)](LICENSE)

A Go client for Ring authentication, device discovery and controls, recordings,
and persistent signaling sessions. Live sessions support SDP/ICE exchange and
PTZ controls. The application supplies its own WebRTC peer for media.

## Install

Requires Go 1.24 or later.

```sh
go get github.com/portpowered/go-ring
```

## Supported operations

These are the public SDK methods. `Client` handles account and HTTP operations;
`SignalingConnection` owns a persistent socket; its live, playback, and push
objects own their respective conversations. The [feature matrix](docs/developer-facing/parity-matrix.md)
distinguishes captured routes from Python-profile and legacy routes.

| Feature | Object and Go calls | Notes |
| --- | --- | --- |
| Client setup and shutdown | `ring.NewClient`, `ring.NewClientWithToken`, `Client.Apply`, `Client.Close` | One client can own several signaling connections. |
| Login and tokens | `Client.Request2FACode`, `Client.Authenticate`, `Client.RefreshToken` | See [token exchange](examples/token-exchange/main.go). |
| Device inventory and lookup | `Client.ListDevices`, `Client.GetDevice`, `Client.GetDeviceDetail` | `GetDeviceDetail` returns the captured v3 wire envelope. |
| Device health and settings | `Client.UpdateDeviceHealth`, `Client.GetDeviceSettings`, `Client.PatchDeviceSettings` | Health can select a doorbell or chime family route. |
| Locations and groups | `Client.ListLocations`, `Client.GetLocation`, `Client.ListLocationGroups`, `Client.ListLocationDevices` | Returns OpenAPI-generated wire models. |
| Motion and device controls | `Client.SetMotionDetection`, `Client.SetLights`, `Client.SetSiren`, `Client.SetVolume`, `Client.SetInHomeChime` | Device and family support varies. |
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

## Examples

Set `RING_ACCESS_TOKEN` before running an example. Use the
[token-exchange example](examples/token-exchange/main.go) or the standalone
[go-ring CLI](cmd/go-ring/README.md) to obtain a token. The CLI also supports
saved-token login, device listing, snapshots, siren control, and interactive
live-view/PTZ. It has its own Go module so its terminal dependencies do not
become library dependencies.

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

Use an access token obtained through `Authenticate`, `RefreshToken`, or the
CLI. The inline examples below assume `ctx`, `accessToken`, and the selected
IDs are available. Reboot sends one device command:

```go
client, err := ring.NewClientWithToken(accessToken)
if err != nil { return err }
defer client.Close()

if err := client.RebootDevice(ctx, ring.DeviceIDRequest{DeviceID: deviceID}); err != nil {
    return err
}
```

To test a chime, use its chime ID and a typed sound kind. The runnable
[chime-sound example](examples/chime-sound/main.go) does not fall back to a
doorbell or camera:

```go
client, err := ring.NewClientWithToken(accessToken)
if err != nil { return err }
defer client.Close()

if err := client.TestSound(ctx, ring.TestSoundRequest{
    DeviceID: chimeID,
    Kind:     ringapimodels.SoundKindDing,
}); err != nil {
    return err
}
```

For a live feed, create a real local WebRTC offer, start a device session,
then apply the answer to the same peer. This uses non-trickle ICE; the full
[rtc_stream example](examples/rtc_stream/rtc_stream.go) also shows trickle ICE,
remote candidates, and session events. The library handles signaling, while
your application owns the Pion peer and media rendering.

```go
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

client, err := ring.NewClientWithToken(accessToken)
if err != nil { return err }
defer client.Close()

conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{})
if err != nil { return err }
defer conn.Close()

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

Keep the session alive with `session.Receive(ctx)` or `session.Wait(ctx)` if no
PTZ operation follows. The runnable stream example applies remote ICE and
keeps reading events until interruption. Its `-trickle` flag sends locally
gathered ICE candidates through `DeviceSession.SendICE`.

Once `StartDeviceSession` returns, call PTZ methods on that `DeviceSession`.
This continues from the live-session code above. Stop a continuous movement
even if the original context is canceled; [rtc_ptz](examples/rtc_ptz/rtc_ptz.go)
contains the complete runnable sequence:

```go
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
_, err = session.StopPTZ(stopCtx, ring.StopPTZRequest{Axis: ring.PanAxis})
return err
```

For `TiltContinuous`, stop with `ring.TiltAxis`; `PanStep` and `TiltStep` send
single-step commands. The deferred closes release the session, signaling
connection, and peer. See the [example index](examples/README.md) for other
flows.

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

The separately vendored Python reference retains its own LGPL-3.0-or-later
license. Its source and tests are a comparison baseline, not a relicensing of
this library.
