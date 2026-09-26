# Small diagnostic CLI

Build a small, opt-in `cmd/go-ring` program for exercising the public library against an account and camera. It is a diagnostic companion, not a second implementation of Ring HTTP or signaling. The existing Go authentication flow, `Client`, `SignalingConnection`, and `DeviceSession` remain the source of behavior. This CLI was outside the original Python parity and replay-coverage gate.

## Commands and expected behavior

| Command | Library call / behavior | Success shown to user |
| --- | --- | --- |
| `go-ring auth login` | `Request2FACode` when needed, then `Authenticate`; prompt for credentials and verification code using the same client instance | Account authenticated; token file path, never token values |
| `go-ring auth status` | Read stored token metadata; optionally perform a harmless inventory request to check validity | Expiry estimate and whether the token worked |
| `go-ring devices list` | `ListDevices` | ID, name, family, and known capabilities; unknown capabilities remain unknown |
| `go-ring snapshot <device-id> --output image.jpg` | `GetSnapshot` | Save the returned bytes and print timestamp/content type |
| `go-ring siren <device-id> on|off` | `SetSiren` | Acknowledged on/off request; do not claim an alarm-system mode changed |
| `go-ring view <device-id> [--player ffplay] [--ice-servers file.json]` | `OpenSignaling`, create a Pion peer, `StartDeviceSession`, then apply answer/ICE and receive video | Connection/media state and a local preview if the negotiated codec/player path works |

Here “alarm” means the existing **device siren** operation. Arming or disarming a Ring Alarm base station is not a supported library operation and must not be implied by this command. The CLI should check the selected device's family/capabilities where known and report unsupported actions clearly.

## Authentication and files

Use the platform's user configuration directory for a default token file, with `--token-file` as an override. Prompt for a password without echo; accept an environment variable only for scripted use, and never put secrets on a command line or in logs. Persist the complete authentication response plus local receipt time. On refresh, atomically replace the file with the newly rotated refresh token before continuing. Reuse the same stable hardware identity across login/refresh/session registration; the CLI may need a small library accessor or an explicit `WithHardwareID` value stored alongside tokens because the currently generated ID is private. On Unix, create owner-only files; on Windows, use an owner-restricted ACL or a protected credential store rather than relying on `0600` alone. `auth logout` deletes the local token record.

Each command loads tokens, creates one client, and performs an explicit refresh when needed. A 401 triggers at most one caller-directed refresh and retry of a **read** request. Do not blindly retry siren or PTZ commands after a timeout: their outcome may be unknown. Never print access tokens, refresh tokens, passwords, verification codes, ICE credentials, SDP, or ticket-bearing URLs.

## Live view and keyboard control

Reuse and extract the offer/ICE handling from `examples/rtc_stream` rather than copying it into a command. Create a receive-only video transceiver, generate a real SDP offer, set local description, wait for ICE gathering (or use the existing trickle path), start a device session, set its answer on the peer, and route incoming candidates. A signaling session being active is not proof that video packets arrive; report peer state, chosen codec, packet count, and first-frame time separately.

The present example discards RTP and has no renderer. For the first CLI preview, bridge the negotiated video RTP to a local `ffplay` process over loopback with an SDP describing the selected codec/payload; reject an unsupported codec with a clear error. Keep the player adapter outside `pkg/ring` so the library does not acquire a UI or process dependency. Do not use captured SDP, TURN credentials, or packet payloads for a live connection. Confirm the minimum working codec/profile with an opt-in camera test before documenting video playback as generally compatible.

With the terminal focused, Left/Right/Up/Down send `PanStep`/`TiltStep`; `q` or Ctrl+C quits. This avoids pretending terminals provide reliable key-release events. Provide an optional `--continuous --speed 0.5` mode: movement starts on a direction key, `Space` sends `StopPTZ`, and an inactivity timer sends stop if no repeat key arrives quickly. Stop the active axis when changing direction, quitting, losing the session, or closing the terminal. A PTZ acknowledgement proves only acceptance of the request, not physical completion or a guaranteed stop if the connection has failed. Keep the device session alive while preview and keyboard input run, and close the session, peer, player, and signaling connection in order on exit or at the 60-minute session limit.

## Implementation slices and verification

1. Add a small standard-library command parser and token store. Reuse `Authenticate`, `RefreshToken`, and `NewClientWithToken`; add only the narrow hardware-ID support required for durable login. Replay-test two-factor login, token rotation, missing/corrupt token files, and secret-free errors.
2. Add devices, snapshot, and siren commands over the existing replay fixtures. Verify exact file bytes and content type, command output, unsupported-device handling, on/off requests, and a timeout with unknown mutation outcome. Keep live-account integration tests opt-in.
3. Extract a reusable example-level Pion session helper, add the player adapter, then add the interactive view. Replay-test SDP/ICE/session teardown and focus keyboard tests on arrow decoding, axis changes, inactivity stop, Space, and exit cleanup. Test the RTP-to-player path with synthetic local packets; do not treat signaling replay as evidence that a live picture renders.
4. Run an explicit, opt-in account/camera smoke test: login/refresh, inventory, snapshot, siren on then off, live video first frame, each arrow, continuous movement and stop, disconnect, and clean exit. Record supported device model, negotiated codec, and any vendor-profile differences without committing credentials or raw captures.

The README should show the six primary commands, token-file location/permissions, `ffplay` as an optional preview dependency, the arrow/Space/q keys, and the difference between a siren command, successful signaling, and actual video/PTZ behavior. Link to this plan and the existing [session design](session-design.md) for SDP and lifecycle details.
