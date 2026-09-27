# Library and verification standard

- **LIB-01** Keep the client interface package as the small public API layer. Put HTTP, WebSocket, WebRTC, and push mechanics in their dependency packages.
- **LIB-02** Keep the CLI in its separate module. Let it consume the public library API.
- **LIB-03** Keep service constants in `internal/protocol`, local lifecycle policy in `internal/signaling`, and generated enum values in schemas.
- **LIB-04** Use one fixture tree at `tests/replay/fixtures`. Label each fixture as captured or synthetic and group it by protocol and behavior.
- **LIB-05** Use replay tests as the primary compatibility measure. Assert the request, response, event order, and public result for each behavior.
- **LIB-06** Split session replay into focused cases for establishment, SDP, ICE, PTZ, ping and pong, expiry, close, push, playback, and failure order.
- **LIB-07** Measure replay, unit, combined, and live integration coverage separately. Do not use an integration result as proof of replay coverage.
- **LIB-08** Test close, RPC, heartbeat expiry, connection failure, queue pressure, and PTZ overlap under the race detector.
- **LIB-09** Use live integration tests only for real endpoints and devices. Make them opt-in and keep credentials out of fixtures.
- **LIB-10** Document each supported operation in the README with a client method and a short inline example. Put authentication before device operations.
- **LIB-11** Explain session setup, SDP and ICE shape, PTZ stop behavior, liveness, and timeout in developer documentation.
- **LIB-12** Use the recording as preferred evidence for a verified operation. Mark synthetic and reference behavior clearly.
- **LIB-13** Keep repository-owned verification tools in Go. Use established external schema tooling where required. Run fixture and schema checks in CI.
