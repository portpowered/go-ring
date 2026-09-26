# Runnable examples

Set `RING_ACCESS_TOKEN` for the device examples. Run
[`token-exchange`](token-exchange/main.go) with `RING_USERNAME` and
`RING_PASSWORD` if you need a token. Set `RING_DEVICE_ID` for reboot and live
session examples, or `RING_CHIME_ID` for the chime sound example. For WebRTC
examples, `RING_ICE_SERVERS_JSON` optionally supplies your ICE server
configuration.

| Example | Command | What it shows |
| --- | --- | --- |
| [Token exchange](token-exchange/main.go) | `go run ./examples/token-exchange` | Requests 2FA when needed and saves tokens. |
| [Enumerate devices](enumerate-devices/main.go) | `go run ./examples/enumerate-devices` | Lists device families and IDs. |
| [Reboot device](reboot-device/main.go) | `go run ./examples/reboot-device` | Sends one reboot request for `RING_DEVICE_ID`. |
| [Chime sound](chime-sound/main.go) | `go run ./examples/chime-sound` | Plays the ding test sound on `RING_CHIME_ID`. |
| [RTC stream](rtc_stream/rtc_stream.go) | `go run ./examples/rtc_stream` | Builds a Pion offer, starts a live session, and handles ICE. Use `-audio` or `-trickle` when needed. |
| [RTC PTZ](rtc_ptz/rtc_ptz.go) | `go run ./examples/rtc_ptz` | Starts a live session, pans right for one second, then stops. |
| [Session push events](session_push_events/session_push_events.go) | `go run ./examples/session_push_events` | Subscribes to push events for one device until interrupted. |
| [Download recordings](download-recordings/main.go) | `go run ./examples/download-recordings` | Retrieves recording history and saves media. |

The RTC examples consume incoming packets but do not render video or provide a
microphone track. Press Ctrl+C to close their sessions and connections.
