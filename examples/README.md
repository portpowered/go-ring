# Session examples

Set `RING_ACCESS_TOKEN` and `RING_DEVICE_ID` before running an example. For
WebRTC examples, `RING_ICE_SERVERS_JSON` optionally supplies your ICE server
configuration.

| Example | Command | What it shows |
| --- | --- | --- |
| [RTC stream](rtc_stream/rtc_stream.go) | `go run ./examples/rtc_stream` | Builds a Pion offer, starts a live session, and handles ICE. Use `-audio` or `-trickle` when needed. |
| [RTC PTZ](rtc_ptz/rtc_ptz.go) | `go run ./examples/rtc_ptz` | Starts a live session, pans right for one second, then stops. |
| [Session push events](session_push_events/session_push_events.go) | `go run ./examples/session_push_events` | Subscribes to push events for one device until interrupted. |

The RTC examples consume incoming packets but do not render video or provide a
microphone track. Press Ctrl+C to close their sessions and connections.
