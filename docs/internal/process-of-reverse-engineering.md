# Porting a library through reference tests and recorded behavior

This is the execution baseline for the go-ring improvement plan. Port behavior
and contracts, then choose idiomatic Go APIs; source code translation alone is
not the goal. The detailed scope and completion gates live in
[the library plan](../library-improvement-plan.md).

1. Pin the reference library as a submodule. Run its existing tests unmodified
   with external networking prohibited. Record the command and actual results.
2. Describe its public features, architecture, HTTP routes, signaling behavior,
   and test coverage. Use the Python tests as the initial behavior checklist,
   including auth regression cases; existing Go authentication remains supported.
3. Pair each behavior with actual sanitized recordings where available. Share
   minimal HTTP exchanges and ordered WebSocket conversations plus schemas.
   Keep device, dialog, signaling-session, and control-session identities
   distinct. Do not add capture manifests or extraction metadata.
4. Run compatible recording inputs through the reference using an adapter.
   Identify whether each test proves request/response transport behavior or only
   model conversion. Mark incompatible routes, absent recordings, and synthetic
   failures explicitly. Never call a converted fixture a captured request.
5. Map each selected behavior to a Go API and tests before implementation:
   Python test/function -> recording file/schema -> Go test -> public operation.
   Extend the matrix for capture-only operations such as PTZ. Prefer demonstrated
   working Go behavior and captured wire evidence when Python differs, and
   document the reason rather than forcing identical implementation details.
6. Implement Go APIs and internals against those tests. Include transport and
   regional overrides, cancellation, malformed responses, session ownership,
   SDP/ICE construction, heartbeat handling, RPC correlation, and lifecycle
   limits. Synthetic negative cases supplement the recorded happy paths.
7. Verify the full reference, contract, Go race, and coverage suites. Publish
   README examples and API/lifecycle/migration documentation that match tested
   behavior. Completion requires the selected feature matrix and planned tests
   to be satisfied, plus at least 90% maintained handwritten library statement
   coverage. Passing a small adapter suite alone does not complete the port.

Use [porting progress](../porting-progress.md) for the current mapping and gaps,
[parity matrix](../parity-matrix.md) for feature decisions, and
[replay format](../replay-format.md) for the committed recording shapes and
repeatable verification command. Unsupported and unverified are useful,
explicit statuses; neither should silently become a support claim.
