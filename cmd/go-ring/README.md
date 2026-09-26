# go-ring diagnostic CLI

This is a separate Go module that imports `github.com/portpowered/go-ring/pkg/ring`. Its terminal input, token file, WebRTC peer, and optional `ffplay` preview are CLI dependencies; the library does not import them. Build from this directory with `go build .` or run with `go run .`.

```text
go-ring auth login
go-ring auth status
go-ring devices list
go-ring snapshot 12345 --output camera.jpg
go-ring snapshot 12345 --output camera.jpg --timeout 45s
go-ring siren 12345 on
go-ring siren 12345 off
go-ring health 12345
go-ring health 12345 --refresh
go-ring sound 67890 ding
go-ring sound 67890 motion
go-ring reboot 12345
go-ring view 12345 --player ffplay
go-ring view 12345 --player none --continuous --speed 0.5
go-ring view 12345 --debug
```

Global `--token-file path` goes before the command. By default, tokens are stored in the user's configuration directory under `go-ring/tokens.json`. Login prompts for a username, password, and a verification code only when challenged. `RING_PASSWORD` and `RING_OTP_CODE` may be used for scripted login. Tokens are never printed. `auth logout` removes the local file. The CLI writes a private file, restricts its Windows ACL to the current user, and atomically replaces it after refresh.

The CLI `snapshot` command starts a live WebRTC view, decodes its first usable video frame with `ffmpeg`, and saves a JPEG. It does not call the legacy snapshot endpoints. `ffmpeg` must be on `PATH`; `--timeout` defaults to 30 seconds, and `--ice-servers file.json` works as it does for `view`. The library's separate `Client.GetSnapshot` method still implements the legacy server snapshot API. `siren` controls a device siren, not Ring Alarm arming. `health` reads current device detail and prints only fields the service returned; `--refresh` also queries the legacy generic health endpoint, which may return no populated fields. `sound` calls the chime test-sound endpoint and requires a chime device; a camera ID does not support it. `reboot` sends the recorded reboot command and reports server acknowledgement, which does not prove the hardware restarted. Mutation timeouts have unknown outcomes; check the device before retrying.

`view` builds a receive-only Pion WebRTC offer, starts a live device session, and applies the answer and remote ICE. The default preview sends depacketized H264 or VP8 video to `ffplay` through standard input, so `ffplay` from FFmpeg must be on `PATH`; `--player none` consumes incoming video and reports its first packet. `--debug` reports ICE and peer connection state, answer application, and PTZ acknowledgements without logging tokens, SDP, or raw signaling payloads. `--ice-servers file.json` accepts a JSON array of Pion ICE server definitions. Terminal arrow keys send one PTZ step each, Space stops continuous movement, and `q` or Ctrl+C closes the session. With `--continuous`, an arrow starts movement at the requested speed and a short idle timeout requests a stop if repeat keys cease. A disconnected session cannot guarantee physical movement stopped.

For local replay testing, global `--api-base`, `--oauth-base`, `--solutions-base`, and `--signaling-url` override service origins. Plain HTTP/WS overrides are accepted only for loopback hosts.
