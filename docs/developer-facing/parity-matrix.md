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
| GET /device_info/v3/devices/{id} | `GetDeviceDetail` returns the captured v3 envelope; `GetDevice` retains list lookup | No corresponding v3 operation found | 42 responses | Separate direct detail from legacy projected lookup |
| GET /location_info/v3/locations; /location_info/v4/locations/{id} | `ListLocations`, `GetLocation` | No matching routes found | 3 / 8 responses | Captured wire models and replay |
| GET /clients_api/doorbots/{id}/health; /clients_api/chimes/{id}/health | `UpdateDeviceHealth` selects family route when `Family` is set; empty family retains generic route | Family-specific health routes | No matching health response identified | Python fixture replay passes; live route unverified |
| PUT /clients_api/doorbots/{id}; /clients_api/chimes/{id} for volume | `SetVolume` uses family-specific route and query settings | Family-specific updates replayed | No matching C1 volume request | Shared synthetic fixture passes; live vendor acceptance unverified |
| PUT /clients_api/doorbots/{id}/floodlight_light_{on,off} | `SetLights` uses family-specific no-body route | async_set_lights / async_set_light | Not identified | Shared synthetic on fixture passes; off lacks capture |
| PATCH /devices/v1/devices/{id}/settings for motion | `GetDeviceSettings`, `PatchDeviceSettings`, and `SetMotionDetection` use captured settings route | async_set_motion_detection | Settings PATCH captured | Shared fixture and captured replay pass |
| POST /clients_api/chimes/{id}/play_sound | `TestSound` uses chime route and `kind` query | async_test_sound | Not identified | Shared synthetic fixture passes; live vendor acceptance unverified |
| PUT /clients_api/doorbots/{id} for in-home chime | `SetInHomeChime` sends one typed query field and description | Existing-doorbell type/enabled/duration setters | Doorbot update observed; chime fields not established | Three shared synthetic fixture cases pass; no matching C1 field capture |
| GET /clients_api/chimes/{id}/linked_doorbots | — | async_get_linked_tree | Not identified | Explicit backlog |
| PUT /clients_api/doorbots/{id}/siren_{on,off} | SetSiren; captured requests replayed | async_set_siren | One each | Keep SetSiren; on-request duration semantics remain unverified because the capture has no query pair while Python sends `duration=30` |
| GET /clients_api/doorbots/{id}/history | `GetDeviceHistory` supports `older_than`, limit, kind | async_history | New history/timeline routes instead | Preserve legacy; EVM exposed separately |
| GET /clients_api/dings/active | GetActiveDings | async_update_dings | Not identified | Keep; distinct from push events |
| GET /clients_api/dings/{id}/recording | GetRecording streams body | recording URL/download helpers | Not identified | Document streaming vs file-writing API distinction |
| GET /clients_api/dings/{id}/share/play | GetRecordingShareURL returns URL | async_recording_url | No matching C1 response | Shared synthetic media fixture passes; server acceptance unverified |
| GET /evm/v3/history/devices; /evm/v2/timeline/devices/{id} | `GetHistoryDevices`, `GetDeviceTimeline` | No matching routes found | 6 / 38 responses | Public recorded wire contracts |
| PUT /clients_api/dings/{id}/favorite; DELETE /clients_api/dings/{id} | `FavoriteRecording`, `DeleteRecording` | No matching helpers found | One each | Public recorded mutations |
| POST /clients_api/snapshots/timestamps; GET /clients_api/snapshots/image/{id} | GetSnapshot triggers, polls freshness, then returns bounded image bytes | async_get_snapshot | C1 instead requests app-snaps /snapshots/next/{id}, with missing responses | Shared synthetic Python-profile replay passes; C1 profile remains separate and unverified |
| GET /groups/v1/locations/{id}/groups | `ListLocationGroups` and separate `ListLocationDevices` | async_update_groups | 34 responses | Group discovery exposed |
| Location /devices vs group /groups/{id}/devices | — | Group-device retrieval/control | Location /devices observed | Separate operations, not equivalent routes |
| PUT /commands/v1/devices/{id}/device_rpc | — | Intercom async_open_door, JSON-RPC | Not identified | Separate intercom scope; not PTZ transport |
| Location users/invitations; intercom settings/history | — | RingOther helpers | No complete matching conversations identified | Explicit intercom parity backlog |
| PATCH /commands/v1/devices/{id}, command_name=reboot | `RebootDevice` | No matching helper found | Flow 178 | Public captured command |
| PUT /duos/v1/devices/{id}/update, entity.live_view_enabled | `SetPersistentLiveViewEnabled` | No matching helper found | Flows 349/352 | Persistent setting; distinct from opening a live session |
| POST prd-api-us.prd.rings.solutions/api/v1/clap/ticket/request/signalsocket | RTC ticket request | RTC ticket request | Different GET route below | Preserve supported bootstrap; compare ticket response families |
| GET prd-api-us.prd.rings.solutions/api/v1/clap/tickets | `GetCapturedTickets` | No matching route found | Three responses | Public C1 bootstrap, separate from POST signaling ticket |

Shared synthetic replay proves the Python-profile request shapes for health, controls, snapshots, and share URLs, but does not establish live vendor compatibility. Successful current/captured routes take precedence over Python alternatives.

## Signaling, RPC and RTC behavior

The C1 socket is wss://api.prod.signalling.ring.devices.a2z.com/ws. HTTP upgrade is bootstrap; messages belong in AsyncAPI. The socket carries multiple logical conversations, not only media signaling.

| Raw message / behavior | Current Go | Python reference | C1 | Target shape and test |
|---|---|---|---|---|
| Connect/close signaling socket | OpenSignaling owns shared transport and children; RTCStream removed | Per RingWebRtcStream | 3 upgrades, 497 messages | SignalingConnection owns transport and children |
| live_view offer | StartDeviceSession with explicit media options | generate; audio/video enabled | 5 sends | StartDeviceSession with explicit media options; no hardcoded mismatch |
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
| close | DeviceSession.Close and parent ownership; legacy StopRTCStream removed | close and remote-close callback | 10 sends / 1 receive | DeviceSession.Close; typed terminal reason; bounded drain |
| playback + SDP/ICE | StartPlayback returns PlaybackSession with answer, ICE send/receive, ping and close; focused captured replay | No playback WebSocket sender found | 6 sends | Live media decoding and recovery remain unverified |
| push_subscribe / ack / heartbeat / event / unsubscribe | SubscribePush returns PushSubscription with typed event and explicit close; focused captured replay | FCM listener, different transport | All observed | Subscription heartbeat cadence and live delivery need field verification |
| Media reception / decoding | Pion example; core client supplies signaling | Requires external WebRTC client | SDP/ICE, no media capability certification | Caller-owned peer connection; SDK handles signaling/control |
| Socket/session reconnection | No complete recovery contract | No complete recovery contract | No proven resumability | Explicit reopen; never replay movement automatically |

## How to maintain this matrix

Maintain operation, reference function/test, actual recording file, schema, and Go test links in this matrix and [porting progress](porting-progress.md). Keep source support, replay support and live verification separate. No capture manifest or generated metadata registry is required. These reviewed Markdown tables are the mapping.

Release scope: existing HTTP methods, Python-profile snapshots and recording share URLs, DeviceSession with keepalive, SDP/ICE, PTZ, and controls, plus focused playback and push conversation replay. Python-only intercom and group features remain visible rather than being implied by a general parity claim. The C1 app-snaps profile still lacks a response contract.

Sources: current Go `pkg/ring`, `pkg/dependencies/rest`, `examples/rtc_stream`, `examples/rtc_ptz`, `examples/session_push_events`; pinned Python `ring.py`, `auth.py`, `generic.py`, `doorbot.py`, `chime.py`, `stickup_cam.py`, `group.py`, `other.py`, `webrtcstream.py`, `listen/eventlistener.py`; C1 local flow/message inspection. See [the pinned Python source](https://github.com/python-ring-doorbell/python-ring-doorbell/tree/486193a80e7c924a0ab14b04d47305e1b36e419e/ring_doorbell).
