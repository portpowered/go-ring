# Wire contract coverage

`contracts_test.go` checks that every recorded HTTP method/path exists in
`api/openapi.yaml` and that each captured signaling message method, including
nested PTZ JSON-RPC methods, exists in `api/asyncapi.yaml`.

The HTTP fixture operations pair with the baseline Python library as follows:

| Recorded operation | Python baseline | Evidence |
| --- | --- | --- |
| Device inventory | `tests/test_ring.py::test_basic_attributes` | captured exchange |
| Device detail | `tests/test_ring.py::test_basic_attributes` | capture-only detail route |
| Device settings read/update | `tests/test_ring.py::test_motion_detection_enable`; `tests/test_other.py::test_other_controls` | recorded settings route is newer than baseline path |
| Siren on/off | `tests/test_ring.py::test_stickup_cam_controls` | captured exchanges |
| History devices | `tests/test_ring.py` history behavior | capture-only list route |
| Device timeline | `tests/test_ring.py` history behavior | capture-only timeline route |
| Signaling ticket | no matching Python test | capture-only bootstrap operation |

The signaling transcript and PTZ methods are capture-only additions: the
baseline [python-ring-doorbell](https://github.com/python-ring-doorbell/python-ring-doorbell) tests do not exercise live signaling
or WebSocket PTZ. The fixtures establish observed message shapes and order;
they do not specify unobserved handshake variants, media success, or SDK timer
policy.

`playback_peer_replay_test.go` takes the recorded playback SDP/ICE/close
envelopes and substitutes fresh descriptions and candidates from two local
WebRTC peers. It removes candidates from both descriptions, delivers them
through playback signaling, and requires an RTP video packet before closing.
This verifies the local playback negotiation path; the packet is synthetic and
does not establish compatibility with Ring's live media servers or selection
of a particular saved recording.
