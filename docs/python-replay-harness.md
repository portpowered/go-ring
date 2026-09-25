# Python reference replay before Go test migration

The first porting gate runs the pinned Python project against files that Go can
read later. The reference submodule stays unchanged. `tools/reference-replay`
contains the Python adapter, while `test/recordings`, `test/fixtures`, and
`test/porting-fixtures` contain the language-neutral inputs. The existing Go
tests still need to be migrated to the shared scenario cases; the Python gate
does not claim that migration is already complete.

Run `python tools/verify_reference.py` from the repository root. It executes the
original Python suite, then all replay tests with Python coverage, then the
protocol and sanitizer suites. The replay stage fails unless at least 95% of
the explicitly selected Python port entry points execute. On the current pin,
the original suite has **40 passing cases** and the replay suite has **72
passing cases**, covering **447/465 selected executable lines (96.13%)**.
`tools/reference-replay/measure_port_scope.py` is the executable denominator:
it names each selected method and reports any missing lines. It excludes code
inside Python's `TYPE_CHECKING` guards, which cannot execute at runtime. The
gate measures lines, not branch coverage or the entire Python package.

The scope includes Python's raw HTTP query, device inventory/session setup,
history, health, controls, recording and snapshot methods, and RTC connection,
SDP/ICE, heartbeat, and close methods. The Python CLI, Firebase push listener,
intercom commands, groups, and OAuth exchange are outside this first port scope.
Existing Go authentication retains its own regression suite. Python has no PTZ
implementation to cover; recorded PTZ commands and results are validated by
AsyncAPI and session-recording tests, and their Go migration remains a separate
test obligation. Coverage over this selected code does not establish full
Python feature parity or live Ring compatibility.

`shared_fixture_harness.py` enumerates all 30 sanitized HTTP exchanges and
five live-view conversations in the two captured WebSocket files. It checks
method, path, query parameters, request JSON, response status, and response
body on HTTP replay. Session replay retains message order and checks Python's
supported offer/answer, ICE, notification, and activation behavior. The
`test/porting-fixtures` JSON files contain clearly synthetic legacy and failure
cases that the capture lacks: ticket bootstrap, control request shapes, media
bytes, remote close, malformed or unknown messages, lifecycle, and HTTP
failures. They contain test data and expected behavior, without hashes,
capture provenance timestamps, extraction metadata, or a capture manifest.
Protocol timestamps used by a scenario remain ordinary test values. The
Python-specific adapter lives outside those files; Go should load the JSON directly.

The recorded GET ticket on the Solutions host is not substituted for Python's
legacy POST signaling ticket. Python's siren-on call adds `duration=30` while
the C1 request does not; that difference remains an explicit comparison, not
a replay pass. The legacy inventory fixture includes a non-intercom `other`
kind that Python filters out and Go retains as a generic device; the adapter
asserts only the shared behavior and records the divergence.

## Original Python test migration index

Each upstream test remains in the baseline. “Replay” names the new fixture
test that exercises the portable library behavior. “Deferred” or “Python only”
is an explicit scope decision, not a passing Go parity test.

| Original test | Replay / scope | Go migration target |
|---|---|---|
| `test_ring::test_basic_attributes` | Legacy inventory and captured C1 device-model replay | `ListDevices`, family conversion, generic device |
| `test_ring::test_chime_attributes` | Legacy inventory, health, controls | Chime model, health, volume, sound |
| `test_ring::test_doorbell_attributes` | Legacy inventory, health, history, media | Doorbell model, history, recordings |
| `test_ring::test_shared_doorbell_attributes` | Legacy inventory and in-home chime replay | Shared doorbell model and chime settings |
| `test_ring::test_stickup_cam_attributes` | Legacy inventory and captured C1 PTZ-camera model replay | Camera model, unknown capability handling |
| `test_ring::test_stickup_cam_controls` | Legacy control requests plus captured siren cases | Lights and siren request tests; on-duration divergence explicit |
| `test_ring::test_light_groups` | Deferred group feature | No stable public Go group abstraction yet |
| `test_ring::test_motion_detection_enable` | Captured settings adapter and legacy control replay | Typed settings PATCH and existing control |
| `test_ring::test_datetime_parse` | Baseline only; Python utility semantics | Go time parsing where used by history models |
| `test_ring::test_sync_queries_from_event_loop` | Python only | No Go sync/async wrapper distinction |
| `test_ring::test_sync_queries_from_executor` | Python only | No Go sync/async wrapper distinction |
| `test_ring::test_sync_queries_with_no_event_loop` | Python only | No Go sync/async wrapper distinction |
| `test_ring::test_set_existing_doorbell_type` | Synthetic in-home chime request replay | `SetInHomeChime` request and validation tests |
| `test_other::test_other_attributes` | Generic identity covered; intercom fields deferred | Generic `Other` model; intercom feature decision |
| `test_other::test_other_controls` | Deferred intercom controls | No public Go intercom control API yet |
| `test_other::test_other_invitations` | Deferred intercom invitations | No public Go invitations API yet |
| `test_other::test_other_open_door` | Deferred intercom door RPC | No public Go unlock API yet |
| `test_listen::test_listen` | Deferred Firebase push transport | Future `EventSubscription`; current event WS is experimental |
| `test_listen::test_active_dings` | Original baseline; current Go raw active-dings test | `GetActiveDings`; push aggregation deferred |
| `test_listen::test_ding_expirey` | Deferred push-event cache | Future push lifecycle test |
| `test_listen::test_listen_subscribe_fail` | Deferred Firebase subscription | Future push failure test |
| `test_listen::test_listen_gcm_fail` | Deferred Firebase registration | Future push failure test |
| `test_listen::test_listen_fcm_fail` | Deferred Firebase registration | Future push failure test |
| `test_cli::test_cli_default` | Python CLI only | No Go CLI in this library |
| `test_cli::test_show` | Python CLI only; inventory/model replay | Public Go device model, no CLI text parity |
| `test_cli::test_devices` | Python CLI only; inventory/model replay | `ListDevices`, no CLI text parity |
| `test_cli::test_list` | Python CLI only; inventory/model replay | `ListDevices`, no CLI text parity |
| `test_cli::test_videos` | Python CLI only; media replay | `GetRecording`, no CLI file naming parity |
| `test_cli::test_auth` | Original baseline; existing Go auth regression suite | Preserve Go auth mechanisms, no CLI migration |
| `test_cli::test_motion_detection` | Captured settings and legacy control replay | Motion settings/control request tests |
| `test_cli::test_listen_store_credentials` | Python CLI and push only | No Go CLI/Firebase credential store |
| `test_cli::test_listen_event_handler` | Python CLI and push only | Future push-event callback semantics |
| `test_cli::test_in_home_chime` | Synthetic in-home chime request replay | `SetInHomeChime` |
| `test_cli::test_open_door` | Deferred intercom CLI action | No public Go unlock API yet |
| `test_cli::test_get_device` | Python CLI selection only; inventory replay | `GetDevice` identity lookup |

The Go migration now consumes the shared POST ticket fixture, all six legacy
control fixtures, all three in-home chime fixtures, and the remote ICE/close variants.
`TestRecordedLiveViewBehaviors` replays the captured offer, session-created,
answer, and camera-started envelopes as focused establishment, ICE-send,
ICE-receive, and remote-termination subtests. `TestRecordedPTZCommandsIndividually`,
`TestRecordedRemoteICEIndividually`, and `TestRecordedHeartbeatPairsIndividually`
select individual captured frames from both streams; the longer conversation
test remains as an ordering and notification regression. All dynamic dialog,
control, and RPC IDs are rebound at the replay boundary.

The Go replay now also consumes the synthetic recording bytes and four HTTP
failure cases. The Python share URL and snapshot fields have no matching Go
API; deferred Python-only features remain explicit parity gaps. A passing Go
test of another route is not counted as migration of one of those fixtures.
