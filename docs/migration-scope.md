# Remaining migration scope

The Python-first replay gate is complete for the selected port scope: 447/465
selected Python lines (96.13%) through portable replay. Go now consumes the
shared legacy ticket, controls, in-home chime, media, snapshot, share URL,
HTTP-failure, and session variants, plus C1 HTTP and focused signaling frames.
The maintained Go library gate is 2050/2272 statements (90.23%). These gates
measure offline implementation behavior, not live Ring compatibility.

## Complete the selected Python-to-Go surface

| Remaining behavior | Current gap | Next evidence and test |
|---|---|---|
| Device health | Go's `UpdateDeviceHealth` still uses a generic `/clients_api/ring_devices/{id}/health`; Python uses family-specific doorbot/chime paths. C1 has no matching health response. | Replay Python family-specific health fixtures through Go, choose an explicit family-aware request, and check missing/null fields. Do not claim C1 confirmation. |
| Legacy history | Go exposes a basic per-doorbot history call; Python also has pagination, `older_than`, filtering, and timezone behavior. C1 uses different EVM history/timeline resources. | Use the existing Python history replay cases as the baseline, add Go pagination/error tests, and keep EVM mapping separate. |
| Chime linkage | Python exposes linked doorbots for a chime; Go has no corresponding method. | Add a portable legacy fixture and a focused Go method/test if chime topology is in the supported device scope. |
| Device family mapping | C1 v3 inventory confirms one PTZ camera; other Go family mapping uses a synthetic inventory fixture. | Add captured family variants or explicit synthetic contract tests for missing/null/unknown fields and shared devices before claiming full model parity. |

`GetSnapshot` now implements Python's POST timestamp trigger/poll and GET image
profile, returning bounded bytes and a timestamp. `GetRecordingShareURL` parses
Python's legacy share/play response. Both use shared synthetic fixtures. The
C1 `app-snaps.ring.com/snapshots/next/{id}` requests have no successful
response in the recording, so they remain a distinct unverified profile.

## Recorded C1 extensions to decide separately

| Candidate | Evidence / decision still needed |
|---|---|
| Direct v3 device detail | Captured `GET /device_info/v3/devices/{id}` responses; current `GetDevice` searches inventory. Decide whether direct detail is a public operation and test its null/capability variants. |
| Locations and groups | Captured location, group, and group-device resources; Python groups use different routes. Define location/group ownership and typed models before adding methods. |
| EVM history and timeline | Captured v2/v3 resources and pagination; normalize independently of legacy history rather than feeding them into its parser. |
| Wider settings and device actions | Captured additional settings PATCH bodies, favorite/delete, reboot, and persistent live-view-enabled update. Each needs an explicit typed request, replay test, and mutation/error contract. |
| Captured GET ticket | C1 has GET `/api/v1/clap/tickets`; the supported Go/Python bootstrap is POST `/api/v1/clap/ticket/request/signalsocket`. Equivalence and handoff to the signaling socket remain unproven. |

## Separate WebSocket work

The live-view `DeviceSession` covers caller SDP/ICE, activation, PTZ, heartbeat,
and close with focused replay. Remaining protocol surface includes typed
camera-options/notification handling and focused tests for pre-answer ICE and
failure ordering. Captured playback conversations and push subscribe/ack/event/
unsubscribe are different logical sessions on the socket; define independent
`PlaybackSession` and `EventSubscription` APIs and replay slices before claiming
support. The existing `/clients_api/ws` event connection is a different,
experimental transport. Reconnection and media transport interoperability
are not established by offline replay.

Python CLI, Firebase push implementation details, intercom invitations/unlock,
and groups were outside the selected 95% port gate. They remain explicit
feature decisions rather than hidden parity failures. Before a compatibility
claim, run opt-in account/hardware tests for supported HTTP, snapshot, and
live-view flows, recording any service-version differences without changing
the captured evidence.
