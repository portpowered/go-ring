# Python parity replay baseline

The first Python migration gate is now the [portable fixture replay harness](python-replay-harness.md):
all 35 original Python test functions have a migration decision, 40 original
cases pass, and 72 replay cases cover 96.13% of explicitly selected Python
entry-point lines. Go now replays the shared ticket, control, in-home chime,
recording-byte, HTTP-failure, and session-variant inputs, plus individual
captured SDP, PTZ, ICE, and heartbeat cases. The table below retains the
protocol comparison and known divergences.

The Python package under `reference/python-ring-doorbell` provides behavioral
tests for the compatibility baseline. Sanitized C1 recordings remain the wire
evidence. Tests should identify both sources and preserve differences in route
or payload shape rather than treating one as proof of the other.

| Behavior | Python baseline | C1 recording | Go coverage | Status |
|---|---|---|---|---|
| Basic device inventory | [`tests/test_ring.py::test_basic_attributes`](../reference/python-ring-doorbell/tests/test_ring.py) expects chime, doorbell, shared doorbell, camera, and other families from the legacy inventory response. | [`device-list.json`](../test/recordings/http/device-list.json) is one v3 inventory containing a captured PTZ indoor camera. | [`TestRecordedDeviceListAndGetDevice`](../test/system/client_recorded_devices_test.go) checks the captured camera; [`TestGetAllDevicesUniformMetadataAndDefaultsAcrossFamilies`](../test/unit/client_public_workflows_test.go) exercises public interface getters using existing Go fixture values. | The fixture-based mapping is synthetic and does not claim all-family v3 wire evidence. Unfamiliar kinds are retained as generic devices with their kind/family metadata; no capabilities are inferred. |
| Stickup camera attributes | [`tests/test_ring.py::test_stickup_cam_attributes`](../reference/python-ring-doorbell/tests/test_ring.py) checks kind/model/capabilities. | [`device-list.json`](../test/recordings/http/device-list.json) contains `stickup_cam_mini_ptz_v1` and its captured attributes. | [`TestRecordedDeviceListAndGetDevice`](../test/system/client_recorded_devices_test.go); Python model adapter: [`test_shared_recordings.py`](../tools/reference-replay/test_shared_recordings.py). | Adapter maps the recorded kind into Python's existing family index to compare model properties. It does not claim Python's legacy request supports the v3 route. |
| Motion detection | [`tests/test_ring.py::test_motion_detection_enable`](../reference/python-ring-doorbell/tests/test_ring.py) guards the model using cached device settings, then sends a PATCH. | [`device-settings-get.json`](../test/recordings/http/device-settings-get.json) supplies the current motion flag; [`device-settings-patch.json`](../test/recordings/http/device-settings-patch.json) captures the same PATCH path, method, and JSON body as the Python call. | [`TestPortableLegacyHTTPControls/doorbell-motion-on`](../test/system/legacy_controls_portable_test.go) and captured settings replay exercise Go's actual PATCH. | Python adapter maps the captured v3 `motion_settings.motion_detection_enabled` value into the legacy cache field, then replays the recorded PATCH response through Python `Auth`. Route, query, and JSON body match; request headers are not compared by this adapter. |
| Siren control | [`tests/test_ring.py::test_stickup_cam_controls`](../reference/python-ring-doorbell/tests/test_ring.py) sends siren off and on commands. | [`siren-off.json`](../test/recordings/http/siren-off.json) matches Python's off request. [`siren-on.json`](../test/recordings/http/siren-on.json) captures the on path with no query pair. | `Client.SetSiren` is covered by [`TestRecordedSettingsAndSirenAcrossRegions`](../test/system/client_recorded_settings_test.go). | The adapter replays the off call through Python `Auth`; a separate request-shape test shows Python's on call adds `duration=30`, which this capture does not contain. The response's duration does not establish the request query, so full on-request parity remains unverified. |
| History | [`tests/test_ring.py::test_doorbell_attributes`](../reference/python-ring-doorbell/tests/test_ring.py) checks legacy per-device history results. | [`device-timeline.json`](../test/recordings/http/device-timeline.json) and [`history-devices.json`](../test/recordings/http/history-devices.json) use EVM routes and response envelopes. | [`TestGetDeviceHistory_Success`](../test/unit/client_recordings_test.go) exercises Go's existing history method against its own legacy fixture. | Python's `/clients_api/doorbots/{id}/history` and its array results are distinct from C1's timeline and grouped-feed contracts; the adapter asserts the gap without feeding EVM data into the legacy parser. |
| Device detail | Python attribute cases read cached inventory objects; the current Go `GetDevice` also searches its list response. | [`device-detail.json`](../test/recordings/http/device-detail.json) records `GET /device_info/v3/devices/{device_id}`. | `GetDevice` is covered via [`device-list.json`](../test/recordings/http/device-list.json). | The detail recording route has no current public Go method and is not counted as supported-route coverage. |

## Signaling and session implementation status

The signaling API is a partial implementation of the recorded application protocol. These statuses describe tested Go behavior; they do not establish successful media transport against Ring hardware.

| Behavior | Go status | Evidence and limits |
|---|---|---|
| Ticket bootstrap and WebSocket ownership | Implemented for the verified legacy POST ticket profile; caller cancellation closes an idle socket. | [`TestPortableLegacyTicketBootstrap`](../test/system/signaling_ticket_portable_test.go) consumes the same synthetic exchange as Python. The captured C1 GET ticket profile is not treated as equivalent. |
| Session start and SDP | Implemented: validates caller offer and device identity, routes `session_created` and SDP by dialog, checks signaling and control-session IDs separately, then activates the session. | [`TestRecordedLiveViewBehaviors/establishment`](../test/system/session_portable_replay_test.go) uses the C1 offer, SDP answer, and session frames; invalid offers remain in [`session_failures_test.go`](../test/system/session_failures_test.go). |
| ICE | Trickle mode validates candidate MID and m-line index against the caller offer before sending. | Focused outbound and remote ICE subtests use the captured offer and shared synthetic remote-ICE variant; [`TestRecordedRemoteICEIndividually`](../internal/signaling/recording_test.go) checks each captured inbound ICE frame. Non-trickle gathering and real media remain caller responsibilities. |
| PTZ RPC | Step commands, separate control-session identity, result correlation, concurrent session routing, and out-of-order replies are implemented. | [`TestRecordedPTZCommandsIndividually`](../internal/signaling/recording_test.go) checks every captured command/ack pair independently; the conversation test retains ordering and notification checks. Continuous movement and zero-speed stop use tracked axis/direction; close attempts best-effort zero-speed stops before signaling close. |
| Heartbeat and expiry | Implemented with negotiated interval, fallback policy, three-interval missed-pong termination, optional shorter MaxAge, `Wait`, and terminal state publication. | [`TestRecordedHeartbeatPairsIndividually`](../internal/signaling/recording_test.go) checks captured ping/pong pairs; the fake-clock virtual-hour and missed-pong tests check expiry without a real wait. No automatic reconnect or session renewal is performed. |
| Queue and socket failures | Bounded event overflow terminates with an exposed backpressure error; queued sends can be canceled by context; pending RPCs fail when the peer closes. | Package tests cover priority for queued close, heartbeat and zero-speed stop messages, with an ordinary write after at most four consecutive priority writes. An already active socket write cannot be preempted. |
| Session and connection close | Child close is idempotent; tracked continuous movement gets a shared bounded best-effort stop budget before close; connection close tears down children and socket. | Focused remote-termination replay uses the shared synthetic close; local system tests check child/parent close and duplicate close. Stop acknowledgements depend on peer availability and are not proof of physical movement cessation. |
| Remote media | Not verified. | The public session accepts caller SDP and returns the signaling answer; it does not create or own a WebRTC peer connection. |

The intentional route difference is explicit: Python's `DEVICES_ENDPOINT` is
`/clients_api/ring_devices`; C1 captured `/device_info/v3/devices`. The adapter's
route test asserts that difference. It feeds the captured device object to
Python's existing device model for attribute comparison, maps one setting into
the legacy cached-device shape, and separately replays two compatible control
requests. Aioresponses replay checks request method, path, query and JSON body;
it does not assert exact request-header parity. Other shape and route gaps stay
explicit in tests instead of being coerced into legacy formats.

From `reference/python-ring-doorbell`, set the root-level environment and
offline guard, then run the scoped Python baseline and adapter:

```powershell
$env:UV_PROJECT_ENVIRONMENT = (Resolve-Path ..\..\.venv-reference).Path
$env:PYTHONPATH = (Resolve-Path ..\..\tools\reference-replay).Path
uv sync --locked --project .
uv pip install --python ..\..\.venv-reference\Scripts\python.exe -r ..\..\tools\reference-replay\requirements.txt
uv run --no-sync pytest -p no:socket -p offline_socket_guard -o addopts= tests -q
uv run --no-sync pytest -p no:socket -p offline_socket_guard -o addopts= ..\..\tools\reference-replay\test_shared_recordings.py -q
```

`tools/reference-replay/requirements.txt` pins shared local contract-test
tooling (`jsonschema` and `PyYAML`) without changing the vendored Python
package's lockfile.

The upstream pytest socket guard blocks Windows asyncio's internal socket-pair
creation before tests start. The adapter's offline guard permits loopback
connections for the Windows event loop and rejects non-loopback name resolution
and connections. The upstream suite's HTTP tests use mocked responses; the
adapter itself does no I/O. The original 40-test suite runs successfully with
this guard in place.

Go's recording-backed client coverage is in
[`test/system/client_recorded_devices_test.go`](../test/system/client_recorded_devices_test.go)
and runs with `go test ./test/system`. Unit configuration coverage is in
[`test/unit/client_endpoints_test.go`](../test/unit/client_endpoints_test.go).
Captured signaling schema checks run with
`uv run --no-sync --project reference/python-ring-doorbell python -m pytest -p no:socket -o addopts= tools/protocols/test_contracts.py -q`.
