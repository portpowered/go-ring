# Portable synthetic replay inputs

These JSON files supplement the sanitized captured exchanges under
`test/recordings`. They are **synthetic** cases for legacy Python behavior and
failure paths absent from the C1 network recording. They are test inputs, not
additional evidence about Ring's current service. No capture manifests,
digests, extraction metadata, or environment labels are used.

`legacy-ticket.json` is a single HTTP exchange for Python's POST signaling
bootstrap. `legacy-control-requests.json` and `legacy-in-home-chime.json` are
arrays of operation cases. Each contains a request method, path, query/body,
and expected response; path placeholders are filled from the loaded device
fixture. `http-failures.json` names synthetic transport or status outcomes.
`media-variants.json` supplies harmless bytes and a share URL for recording
and snapshot tests. `session-variants.json` holds application messages missing
from the capture. `session-lifecycle.json` holds SDP/ICE and failure variants.

The Python runner in `tools/reference-replay` consumes these alongside the
actual captured files. A Go runner should read the same JSON and implement its
own request/response and session adapters; it should not import Python code.
