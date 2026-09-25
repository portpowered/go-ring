# Stateful signaling and device sessions

Implementation contract with current APIs and remaining requirements. The connection/device-session API and bounded priority writer are implemented and locally tested; live media interoperability remains a separate gap. The writer prioritizes queued safety messages while preserving bounded access for ordinary commands; it cannot preempt a socket write already in progress. See [porting progress](porting-progress.md) for the test mapping. This document supersedes the earlier plan to retain RTCStream as the primary name. Optimize feature clarity; retain existing authentication mechanisms. Companion: [parity matrix](parity-matrix.md), [implementation plan](library-improvement-plan.md).

## Public objects and ownership

| Object | Responsibility | Lifetime |
|---|---|---|
| Client | Existing auth, HTTP device/settings/recording operations; creates connections | Application scope |
| SignalingConnection | Authenticated WebSocket, one reader/writer, routing, child registry | Explicitly opened/closed, independent of one media stream |
| DeviceSession | One live device interaction: SDP/ICE negotiation, media controls, PTZ and session keepalive | Up to 60 minutes; can end earlier |
| EventSubscription | Subscription ID, push heartbeat, events and unsubscribe | Independent of DeviceSession; future implementation scope |
| PlaybackSession | Recorded playback SDP/ICE and playback controls when verified | Separate future scope; don't fabricate controls from live-view APIs |
| Caller peer connection | ICE/DTLS/SRTP, codecs, tracks, media transport | Application owns and closes it |

Closing a child leaves siblings and the socket alive. Closing a connection terminates all children; closing the client terminates its owned connections. The session has no claim to own the caller's peer connection. Helpers/examples wire terminal session events to peer cleanup. A socket may stay usable after a device session reaches 60 minutes; this is not a token lifetime or connection timeout.

```go
// Implemented public signatures; types live in pkg/ring.
func (c *Client) OpenSignaling(ctx context.Context, req OpenSignalingRequest) (*SignalingConnection, error)
func (c *SignalingConnection) StartDeviceSession(ctx context.Context, req StartDeviceSessionRequest) (*DeviceSession, error)
func (s *DeviceSession) Answer() SessionDescription
func (s *DeviceSession) SendICE(ctx context.Context, req ICECandidateRequest) error
func (s *DeviceSession) PanStep(ctx context.Context, req PanStepRequest) (*PTZResult, error)
func (s *DeviceSession) TiltStep(ctx context.Context, req TiltStepRequest) (*PTZResult, error)
func (s *DeviceSession) PanContinuous(ctx context.Context, req PanContinuousRequest) (*PTZResult, error)
func (s *DeviceSession) TiltContinuous(ctx context.Context, req TiltContinuousRequest) (*PTZResult, error)
func (s *DeviceSession) StopPTZ(ctx context.Context, req StopPTZRequest) (*PTZResult, error)
func (s *DeviceSession) SetMicrophone(ctx context.Context, req SetMicrophoneRequest) error
func (s *DeviceSession) SetStreamOptions(ctx context.Context, req SetStreamOptionsRequest) error
func (s *DeviceSession) Receive(ctx context.Context) (*SessionEvent, error)
func (s *DeviceSession) Wait(ctx context.Context) error
func (s *DeviceSession) State() SessionState
func (s *DeviceSession) Close() error
func (c *SignalingConnection) Close() error
```

Pan commands use `PanDirection` (`PanLeft`, `PanRight`) and tilt commands use
`TiltDirection` (`TiltUp`, `TiltDown`). Existing untyped string literals such
as `Direction: "LEFT"` remain assignable, while named string variables should
use the matching direction type. `PTZResult` exposes `SessionID`, `Timestamp`,
and `Version` from the captured acknowledgement and retains the full result in
`Raw` for forward-compatible fields. Remote command failures are `*RPCError`
values and can be inspected with `errors.As`.

StartDeviceSessionRequest contains device ID, a typed SDP offer, media options, ICE mode and optional shorter maximum duration. It does not require callers to manufacture session IDs, RPC IDs, timestamps or heartbeat messages. An open/start context governs the returned object's lifetime. Command/Receive/Wait contexts govern only the individual wait or command. Document this distinction in examples: don't pass a short-lived HTTP request context when intending to retain the session.

StartDeviceSession sends the offer, obtains a validated answer and signaling ID, and completes the defined signaling activation prerequisites. It returns a handle ready for signaling/control; it does not assert that media is flowing. The caller applies Answer to its peer. Candidate events are buffered until consumed. Trickle candidates produced during start are buffered by the example/adapter and flushed through SendICE after the handle is returned; test this against a scripted peer rather than assuming arbitrary timing works.

The connection routes by dialog/session/subscription IDs; DeviceSession routes RPC replies by nested command ID. PTZ params.sessionId is a separate identity domain from outer body.session_id. The implementation obtains the independent control identity from the answer profile and rejects an answer without it. Unknown/stale identifiers cannot complete another session's request.

## SDP construction and exchange

Generate SDP through a real WebRTC peer implementation, such as the Pion peer already used by the example. Treat SDP as a structured description bound to that peer's ICE credentials, DTLS certificate and transceivers. Never copy a captured SDP into a production connection. Offer creation, local description, remote answer and candidate application follow the peer's negotiation state. [JSEP](https://www.rfc-editor.org/rfc/rfc8829.html)

Document two clearly named example profiles:

| Profile | Transceivers / options | Evidence |
|---|---|---|
| Receive video | Video recvonly; stream video enabled, audio disabled | Existing Go example; requires contract/live validation per device |
| Live view with audio/talk capability | Audio sendrecv, video recvonly; consistent media flags; application data section as needed by the chosen profile | C1 flow 21 message 16 has audio MID 0, video MID 1, application MID 2 |

C1 playback offers have a different multi-section shape (first example: five audio sections, video and application). Do not reuse their m-line indexing as the live-view profile. C1 offers advertise more codecs than a minimal Go peer may implement; advertised is not synonymous with selected or required. Extract per-offer/answer pairs before claiming a minimum interoperable codec profile.

Required construction recipe for examples and documentation:

1. Configure the peer's supported codecs and ICE servers; register remote track/state handlers. Obtain credentials through supported configuration, never a hardcoded captured TURN credential.
2. Add the profile's audio/video transceivers before creating the offer. Add a data channel only when the profile needs it; PTZ itself is observed on WebSocket, not proven to require a data channel.
3. Call CreateOffer, then SetLocalDescription. For non-trickle mode, await gathering completion under a deadline and use the updated LocalDescription. For trickle mode, queue candidate callbacks and preserve each candidate's MID and m-line index.
4. Send the description using StartDeviceSession. Apply Answer as the remote description exactly once. Apply remote ICE events after the remote description exists; queue bounded early candidates.
5. Keep the DeviceSession alive while media/control is used. Send PTZ or microphone/stream options through its typed methods. Close the session and caller peer separately.

The public SessionDescription uses type=offer or answer and non-empty SDP text. StartDeviceSession validates and parses the caller's offer; the returned answer is parsed and normalized by matching media sections by MID. These checks establish structural consistency, not codec/media interoperability:

| SDP element | Documentation / validation rule |
|---|---|
| v/o/s/t and m-lines | Parse via an SDP parser; preserve media-section order and origin/version semantics |
| a=mid and a=group:BUNDLE | Unique MID per section, referenced bundle members exist; candidates map to the correct MID/index |
| a=ice-ufrag / a=ice-pwd | Peer-generated credentials at the applicable scope; never expose them in normal logs |
| a=fingerprint / a=setup | Bound to peer certificate/DTLS role; generated by the peer, not copied from fixtures |
| a=rtpmap / a=fmtp / payload lists | Consistent codec/payload references, RTX apt mapping and codec parameters; don't fix H264 profile or payload number from one capture |
| Media direction / rtcp-mux | Match intended receiving/talk behavior and negotiated response; handle rejected sections explicitly |
| a=candidate and trickle events | Preserve grammar, MID and index; distinguish embedded vs trickled candidates; end-of-candidates mapping requires a verified Ring wire representation |
| Application section | Preserve SCTP attributes when present; don't require it for all devices without evidence |
| Vendor a=x-* fields | Preserve unknown extensions; maintain an observed/required/optional inventory instead of guessing requirements |

For a recvonly offer, the answer direction must be sendonly or inactive. Go parses the SDP and applies the observed recvonly/sendrecv workaround to the matching media section by MID, while retaining unknown attributes. The normalized answer is returned; the implementation does not preserve a separate original/normalized diagnostic pair. Live media interoperability remains unverified. [Offer/answer direction rules](https://www.rfc-editor.org/rfc/rfc3264.html)

The `examples/device-session` program generates real peer offers and supports non-trickle and `-trickle` modes; parser and replay tests use synthetic SDP. A diagram or hand-edited SDP fragment is explanatory only and must not be presented as runnable connection credentials. A playback example remains deferred until its contract is implemented.

## Internal state and timers

Connection states: connecting -> open -> closing -> closed/failed. Device-session states: negotiating -> activating -> active -> closing -> closed/expired/failed. Track media readiness separately in the caller peer. Session close must be possible from every state.

The **60-minute maximum is an intended SDK policy requested for this design**. C1's populated socket conversations span approximately 762.79 and 362.88 seconds; they cannot demonstrate a vendor-enforced one-hour lifetime. Python's caller keep_alive timeout (default 30 seconds) is a different concept. The new Go DeviceSession enforces this maximum; the legacy RTC API and Python baseline do not establish a vendor limit.

| Timer | Proposed rule | Evidence / origin |
|---|---|---|
| Socket handshake | 10 seconds, bounded by parent context | Existing Go default; retain as explicit configuration |
| Session negotiation | 30 seconds total for offer/answer/activation | Existing Go answer wait is 30 seconds; activation coverage is new policy |
| Session maximum age | 60 minutes from accepted StartDeviceSession invocation, including negotiation; caller may shorten, not extend | Requested SDK policy; monotonic clock, independent of activity |
| Application heartbeat | Honor verified session interval; C1 interval=10 corresponds to roughly 10-second median ping spacing | 11 SDP responses with interval=10; 74 ping/pong pairs |
| Missing interval fallback | 5 seconds for the legacy profile; malformed/out-of-range interval fails negotiation rather than silently overriding | Existing Go/Python cadence; policy must be version/profile-specific |
| Pong deadline | Three heartbeat intervals since last valid matching pong; initialize grace at activation | Proposed SDK policy, not captured server guarantee |
| PTZ command result | Default 10 seconds or caller deadline, whichever is earlier | Proposed bounded wait; timeout means outcome unknown |
| Close/drain | Up to 2 seconds within remaining lifetime for best-effort stop/session-close, then force cleanup | Proposed SDK policy; no unbounded waits |
| Push heartbeat | Separate subscription timer; extract cadence and ack semantics independently | C1 has 95 push_heartbeat messages; not a session pong substitute |

Use an injected monotonic clock and one deadline-aware scheduler or equivalent bounded timers. Pongs do not extend the 60-minute expiry. Unrelated messages and WebSocket control pong frames do not refresh the session application-pong deadline. Send heartbeat only for established sessions; validate device/session/dialog association before accepting a pong. Permit only the scheduling/association semantics supported by the capture; no invented ping nonce requirement.

At hard expiry, reject new sends, return ErrSessionExpired (inspect with errors.Is) to Wait and pending commands, and release routing entries/queues/timers. Start any bounded graceful stop/close before the hard deadline; force teardown at the deadline. Parent cancellation, peer close or socket failure may terminate earlier. Do not automatically reauthenticate, reopen, renegotiate or replay PTZ to evade the limit. Applications explicitly create a fresh session/peer as needed.

The writer prioritizes queued close, ping/pong, and zero-speed continuous-PTZ messages. When both queues remain nonempty, at least one ordinary command runs after four priority writes. A write already in progress cannot be preempted. Pending RPCs are registered before enqueueing; cancellation before a queued write starts removes it, while a timeout after sending may leave the camera action's outcome unknown. Continuous movement stop is a zero-speed RPC with the tracked axis/direction; if the connection is gone, do not claim the camera stopped. The reader routes messages without invoking user callbacks or waiting for consumer I/O. Bounded event overflow produces an explicit terminal error rather than blocking heartbeats.

## Required acceptance tests

- C1 live-view + PTZ transcript with interleaved SDP/ICE, pings, RPC results and limit notifications; preserve distinct identity domains.
- Two device sessions and one subscription on one scripted socket: no cross-routing; child close/expiry leaves siblings working; connection close terminates all.
- Automatic idle keepalive at negotiated interval; missing/invalid intervals; unmatched/stale pongs; three-interval deadline; writer backpressure without heartbeat starvation.
- Fake clock immediately before/at/after 60 minutes: activity never extends age, shorter caller limit wins, blocked sends/RPC waits terminate, all timers/routes released.
- RPC replies out of order, duplicate/late replies, command-context cancellation, parent cancellation, socket failure and simultaneous Close calls.
- SDP profile generation with real local peer objects; parser validation, non-trickle and trickle ordering, early remote candidates, multiple media sections and narrowly scoped direction correction.
- No-network CI replay plus optional separately authorized live media interoperability checks. A parsed SDP or completed signaling handshake is not a successful decoded video test.

## Breaking migration

The signaling connection also supports two captured sibling scopes. Call
`StartPlayback(ctx, StartPlaybackRequest{DeviceID, Offer, EntryPoint})` with a
caller-created WebRTC offer to obtain `PlaybackSession.Answer()`. Feed remote
`ice` events from `Receive` into the peer, send local candidates through
`SendICE`, and close the playback handle to send the captured `close` shape.
The SDK sends negotiated application pings, accepts pongs only for that device
and signaling session, terminates after three missed intervals, and ends
playback after 60 minutes.
Playback does not expose camera/PTZ controls.

Call `SubscribePush(ctx, []PushFilter{...})` on the same connection. The filter
uses `filter_identifier`, `notification_scope`, `notification_type`, and
optional `filters.doorbot_ids`, as seen in both captures. `Receive` returns a
`PushEvent` with the original JSON payload; `Close` sends
`push_unsubscribe`. The subscription heartbeat runs every 30 seconds while
the connection remains open. This interval is SDK policy, pending live cadence
confirmation; the captures preserve message order but not reliable timing.
These handles do not imply reconnect/resume behavior after socket failure.

The Go HTTP client in `pkg/generatedhttp` and signaling models in
`pkg/generatedsignaling` come from `api/openapi.yaml` and `api/asyncapi.yaml`.
Regenerate with `make generate-api` when a schema changes; the command invokes
the pinned `oapi-codegen` and Modelina CLIs directly. Keep the domain session
types in `pkg/ring`.

Replace StartRTCStream/RTCStream with OpenSignaling -> StartDeviceSession/DeviceSession. StopRTCStream is removed; new code closes the session handle. Replace OnICECandidate/GetSDPAnswer with SendICE/Answer. Use SubscribePush for captured signaling push events. The experimental ConnectEvents path remains a separate protocol. Remove the giant ClientInterface from the new API in favor of concrete types and small consumer-defined interfaces.

Keep the module/import layout stable where it aids migration, but do not add aliases solely to preserve misleading names. Release with explicit breaking-change notes and a method-by-method migration table. If the module has reached v1, follow a new major version/module path; if still pre-v1, announce the incompatible release clearly. Auth remains unchanged. Migrate examples, docs, mocks and tests in the same release.


## Executable offer example

[examples/device-session](../examples/device-session/main.go) creates a Pion
peer, adds receive-only video (and optional send/receive audio), gathers ICE,
and sends the updated local offer in non-trickle mode. Set `RING_ACCESS_TOKEN`
and `RING_DEVICE_ID`, then explicitly run `go run ./examples/device-session`.
Use `-audio` for the audio/video profile; attach a microphone track and renderer
in your application. This example consumes packets but does not render video
or record audio. `RING_ICE_SERVERS_JSON` optionally supplies application-owned
ICE configuration; no captured TURN credentials are reused.

The example applies the negotiated answer before consuming remote ICE events
and separately closes the peer, child session, connection, and client. Its Go
tests generate both offer profiles using real local Pion objects with no Ring
credentials or vendor traffic. Use `-trickle` to queue local candidates while the session starts, then flush
them through SendICE after activation. The queue is bounded and cancels the
example on overflow; candidate identity is preserved and no speculative
end-of-candidates message is sent. Local Pion tests verify the buffered
candidates reference the generated offer; scripted peers test wire delivery.
