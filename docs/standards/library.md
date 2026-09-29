# Library and verification standard

- **LIB-01** Keep the client interface package as the small public API layer. Put HTTP, WebSocket, WebRTC, and push mechanics in their dependency packages.
- **LIB-02** Keep the CLI in its separate module. Let it consume the public library API.
- **LIB-03** Keep service constants in `internal/protocol`, local lifecycle policy in `internal/signaling`, and generated enum values in schemas.
- **LIB-04** Use one fixture tree at `tests/replay/fixtures`. Label each fixture as captured or synthetic and group it by protocol and behavior.
- **LIB-05** Use replay tests as the primary compatibility measure. Store paired request and response expectations, or ordered bidirectional message transcripts. Match method, origin, escaped path, repeated query values, relevant headers, and body or frame payload before returning a response; check response status, headers, body, event order, and public result. Reject unexpected or duplicate calls, assert every expected exchange was consumed, and never return a fallback response after a mismatch. Give redacted or volatile fields explicit format or decoded-value match rules.
- **LIB-06** Split session replay into focused cases for establishment, SDP, ICE, PTZ, ping and pong, expiry, close, push, playback, and failure order.
- **LIB-07** Measure replay, unit, combined, and live integration coverage separately. Do not use an integration result as proof of replay coverage.
- **LIB-08** Test close, RPC, heartbeat expiry, connection failure, queue pressure, and PTZ overlap under the race detector.
- **LIB-09** Use live integration tests only for real endpoints and devices. Make them opt-in and keep credentials out of fixtures.
- **LIB-10** Document each supported operation in the README with a client method and a short inline example. Put authentication before device operations.
- **LIB-11** Explain session setup, SDP and ICE shape, PTZ stop behavior, liveness, and timeout in developer documentation.
- **LIB-12** Use the recording as preferred evidence for a verified operation. Mark synthetic and reference behavior clearly.
- **LIB-13** Keep repository-owned verification tools in Go. Use established external schema tooling where required. Run fixture and schema checks in CI.
- **LIB-14** Keep the public client, README, examples, and reusable library template independent of any consuming application or backend. Put consumer-specific adapters and migration plans in the consumer's repository.
- **LIB-15** Show Go version, CI, replay coverage, latest release, Go Reference, and license badges in the README. Link each badge to the corresponding live report or release.
- **LIB-16** When the library has API schemas, generate its API documentation website from those checked-in schemas and publish it to GitHub Pages through CI. Link the published site from the README and verify the publishing workflow as part of release readiness.
- **LIB-17** Add customer guide pages for supported operations and multi-step flows to the GitHub Pages site. Explain client calls, inputs, outputs, session lifecycle, and errors using the site's standard renderer; verify the pages and navigation in the generated site.
- **LIB-18** Inventory active network calls made inside pinned dependencies. Bind HTTP requests and responses to separate external schemas and binary frames to their source protocol files. Make every dependency socket injectable for offline paired replay; a forwarding HTTP transport does not cover a separate TLS connection.
