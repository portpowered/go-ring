# Feature and protocol parity

Implementation and evidence matrix. Compare behavior and exact wire operations, not similarly named methods. “Implemented” means source exists, not live-certified support. Python is pinned at `486193a80e7c924a0ab14b04d47305e1b36e419e`. C1 is the native capture identified in [the plan](library-improvement-plan.md). A dash means no implementation/evidence identified in the inspected source/capture, not proof that Ring lacks the feature.

## HTTP operations

Paths below are relative to api.ring.com unless another host is given. “Divergent” means different, not defective. Prefer verified working Go behavior and direct capture evidence over Python. Investigate with contract tests; do not change a working route merely to match Python. Record intentional differences as accepted outcomes. Implemented additions are identified below; remaining target names are proposals. See [porting progress](porting-progress.md) for exact tests and files.

| Feature / raw operation | Current Go | Python reference | C1 capture | Target / decision |
|---|---|---|---|---|
| OAuth POST oauth.ring.com/oauth/token; OTP and refresh | Authenticate, Request2FACode, RefreshToken | Auth token/2FA/refresh flows | No token exchange identified | Keep existing auth mechanisms; regression tests |
| POST /clients_api/session | RegisterSession via ensureSession | async_create_session | Not identified | Keep registration separate from signaling session |
| GET /device_info/v3/devices | ListDevices already uses v3 | Uses legacy discovery | 16 responses | Preserve v3; improve normalization, do not reimplement it as a new feature |
| GET /clients_api/ring_devices | Constant exists; current discovery uses v3 | async_update_devices | Browser-proxy route only | Python legacy adapter for parity comparison; no blind fallback |
| GET /device_info/v3/devices/{id} | GetDevice currently searches list | No corresponding v3 operation found | 42 responses | Client.GetDevice direct lookup after schema verification |
| GET /location_info/v3/locations; /location_info/v4/locations/{id} | — | No matching routes found | 3 / 8 responses | Location APIs, explicitly versioned wire schemas |
| GET /clients_api/doorbots/{id}/health; /clients_api/chimes/{id}/health | Divergent: uses /clients_api/ring_devices/{id}/health | Family-specific health routes | No matching health response identified | Correct/verify GetDeviceHealth routing per family |
| PUT /clients_api/doorbots/{id}; /clients_api/chimes/{id} for volume | Divergent: /clients_api/ring_devices/{id}/volume | Family-specific update bodies | Doorbot update observed; volume semantics not yet established | Typed SetVolume with verified request body |
| PUT /clients_api/doorbots/{id}/floodlight_light_{on,off} | Divergent: /clients_api/ring_devices/{id}/lights | async_set_lights / async_set_light | Not identified | Verify/fix SetLights routing |
| PATCH /devices/v1/devices/{id}/settings for motion | GetDeviceSettings / PatchDeviceSettings; legacy SetMotionDetection retains its existing route | async_set_motion_detection | Settings patches present; classify each body | Typed settings, explicit field-level evidence |
| POST /clients_api/chimes/{id}/play_sound | Divergent: /clients_api/ring_devices/{id}/test_sound | async_test_sound | Not identified | PlayChimeSound with verified payload |
| PUT /clients_api/doorbots/{id} for in-home chime | Divergent: /clients_api/ring_devices/{id}/in_home_chime | Existing-doorbell type/enabled/duration setters | Doorbot update present; field-level comparison pending | Typed chime settings |
| GET /clients_api/chimes/{id}/linked_doorbots | — | async_get_linked_tree | Not identified | Explicit backlog |
| PUT /clients_api/doorbots/{id}/siren_{on,off} | SetSiren; captured request shape replayed | async_set_siren | One each | Add SetSiren; compare duration semantics before declaring full parity |
| GET /clients_api/doorbots/{id}/history | GetDeviceHistory | async_history | New history/timeline routes instead | Preserve legacy and compare normalized results |
| GET /clients_api/dings/active | GetActiveDings | async_update_dings | Not identified | Keep; distinct from push events |
| GET /clients_api/dings/{id}/recording | GetRecording streams body | recording URL/download helpers | Not identified | Document streaming vs file-writing API distinction |
| GET /evm/v3/history/devices; /evm/v2/timeline/devices/{id} | — | No matching routes found | 6 / 38 responses | New history/timeline adapters |
| PUT /clients_api/dings/{id}/favorite; DELETE /clients_api/dings/{id} | — | No matching helpers found | One each | Separate candidate recording mutations |
| Snapshot timestamp/image routes | — | async_get_snapshot | app-snaps /snapshots/next/{id}, missing responses | Backlog; different routes and insufficient response evidence |
| GET /groups/v1/locations/{id}/groups | — | async_update_groups | 34 responses | Group discovery backlog |
| Location /devices vs group /groups/{id}/devices | — | Group-device retrieval/control | Location /devices observed | Separate operations, not equivalent routes |
| PUT /commands/v1/devices/{id}/device_rpc | — | Intercom async_open_door, JSON-RPC | Not identified | Separate intercom scope; not PTZ transport |
| Location users/invitations; intercom settings/history | — | RingOther helpers | No complete matching conversations identified | Explicit intercom parity backlog |
| PATCH /commands/v1/devices/{id}, command_name=reboot | — | No matching helper found | Flow 178 | Optional explicit RebootDevice operation |
| PUT /duos/v1/devices/{id}/update, entity.live_view_enabled | — | No matching helper found | Flows 349/352 | Persistent setting; distinct from opening a live session |
| POST prd-api-us.prd.rings.solutions/api/v1/clap/ticket/request/signalsocket | RTC ticket request | RTC ticket request | Different GET route below | Preserve supported bootstrap; compare ticket response families |
| GET prd-api-us.prd.rings.solutions/api/v1/clap/tickets | — | No matching route found | Three responses | Document C1 bootstrap separately; don't assume interchangeability |

The divergent Go routes are source-level findings in `pkg/dependencies/rest/control.go` and `devices.go`, compared with Python `const.py`, `doorbot.py`, `chime.py`, and `stickup_cam.py`. Existing Go tests can validate a self-consistent mock route without establishing vendor compatibility. Prioritize exact request matching for these rows. Suggestions to correct/verify routing mean correction only if evidence shows the current route fails; successful current/captured routes take precedence over Python alternatives.

## Signaling, RPC and RTC behavior

The C1 socket is wss://api.prod.signalling.ring.devices.a2z.com/ws. HTTP upgrade is bootstrap; messages belong in AsyncAPI. The socket carries multiple logical conversations, not only media signaling.

| Raw message / behavior | Current Go | Python reference | C1 | Target shape and test |
|---|---|---|---|---|
| Connect/close signaling socket | OpenSignaling owns shared transport and children; legacy RTCStream remains | Per RingWebRtcStream | 3 upgrades, 497 messages | SignalingConnection owns transport and children |
| live_view offer | StartDeviceSession with explicit media options; legacy StartRTCStream remains | generate; audio/video enabled | 5 sends | StartDeviceSession with explicit media options; no hardcoded mismatch |
| session_created | DeviceSession validates device/dialog/session identity | Stores signaling ID | 5 receives | DeviceSession identity, distinguish from control ID |
| sdp answer | Typed Answer; parsed MID-based direction normalization and offer validation | Return or callback; normalization | 11 receives including playback | Typed Answer and readiness; validate negotiated profile |
| ice | SendICE with MID/index validation; Receive supplies remote wire events | Send plus callback/collection | 19 sends, 36 receives | SendICE and typed remote-ICE events; bounded pre-answer buffering |
| activate_session | Explicit activation sequence, waits for camera_started | Sends on answer | 5 sends | Explicit activation transition; ready != first answer alone |
| camera_started / notification | Activation readiness and bounded session events | Camera-started logging and notification handling | Both present | Typed readiness/status events |
| camera_options | Sends stealth_mode setting after notification | Similar handling | 2 sends | Typed session control, documented write/ack semantics |
| mic_enable / stream_options | SetMicrophone / SetStreamOptions, tested at local peer | No corresponding sender found | 9 / 7 sends | SetMicrophone / SetStreamOptions on DeviceSession |
| PTZ.Pan.Step / PTZ.Tilt.Step inside rpc | Typed PanStep / TiltStep with correlated acknowledgements | No PTZ RPC sender found; recognizes PTZ device kind | 21 / 5 sends | DeviceSession.PanStep / TiltStep |
| PTZ.Pan.Continuous / PTZ.Tilt.Continuous | Typed continuous methods / StopPTZ; tracked zero-speed teardown | No sender found | 16 / 6 sends, includes zero speed | Continuous movement and explicit stop contract |
| RPC result / PTZ.Pan.Halted | Typed results/RPCError; unsolicited events via Receive | No handler found | 48 results / 2 halted | Pending-call correlation; unsolicited limit events |
| Zoom / presets / absolute positioning | — | No corresponding API found | Not observed | Out of supported target until evidenced |
| ping / pong | Negotiated interval, matching-pong deadline and tested heartbeat failure | 5-second pinger; caller keep_alive age limits sending | 74 / 74, advertised interval 10 | Automatic liveness, deadline-driven failure, no caller ping loop |
| Session lifetime 60 minutes | DeviceSession hard maximum including negotiation; fake-clock core tests | No 60-minute maximum identified | Not established by short captures | Explicit requested SDK policy, fake-clock expiry tests |
| close | DeviceSession.Close and parent ownership; legacy StopRTCStream now acts on registered IDs | close and remote-close callback | 10 sends / 1 receive | DeviceSession.Close; typed terminal reason; bounded drain |
| playback + SDP/ICE | — | No playback WebSocket sender found | 6 sends | Separate PlaybackSession scope, not implicit LiveView mode |
| push_subscribe / ack / heartbeat / event / unsubscribe | Experimental unrelated /clients_api/ws event API | FCM listener, different transport | All observed | EventSubscription on SignalingConnection; transport parity remains partial |
| Media reception / decoding | Pion example; core client supplies signaling | Requires external WebRTC client | SDP/ICE, no media capability certification | Caller-owned peer connection; SDK handles signaling/control |
| Socket/session reconnection | No complete recovery contract | No complete recovery contract | No proven resumability | Explicit reopen; never replay movement automatically |

## How to maintain this matrix

Maintain operation, reference function/test, actual recording file, schema, and Go test links in this matrix and [porting progress](porting-progress.md). Keep source support, replay support and live verification separate. No capture manifest or generated metadata registry is required. These reviewed Markdown tables are the mapping.

Release scope: first make existing HTTP methods accurate and establish DeviceSession with keepalive, SDP/ICE, PTZ and controls. Playback and push subscriptions receive explicit types and backlog entries, with implementation gated on complete conversation fixtures. Python-only intercom/groups/snapshot features stay visible rather than being implied by a general parity claim.

Sources: current Go `pkg/ring`, `pkg/dependencies/rest`, `examples/rtc-stream`; pinned Python `ring.py`, `auth.py`, `generic.py`, `doorbot.py`, `chime.py`, `stickup_cam.py`, `group.py`, `other.py`, `webrtcstream.py`, `listen/eventlistener.py`; C1 local flow/message inspection. See [the pinned Python source](https://github.com/python-ring-doorbell/python-ring-doorbell/tree/486193a80e7c924a0ab14b04d47305e1b36e419e/ring_doorbell).
