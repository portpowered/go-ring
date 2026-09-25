# HTTP OpenAPI coverage

[`api/openapi.yaml`](../api/openapi.yaml) validates every sanitized HTTP recording under `test/recordings/http`, including response bodies and captured request bodies. Settings wire responses remain extensible, while the documented typed Go surface currently covers only `motion_settings.motion_detection_enabled`. Captured settings PATCH variants for motion, video, general, and volume settings remain part of the broader wire schema; their presence does not imply corresponding Go methods.

The spec names captured query parameters for timeline, history-device, location detail, captured ticket GET, and recording delete operations. They are marked optional because a single captured request cannot establish which parameters the service requires. Captured query values remain evidence examples, not exhaustive accepted-value lists.

`RingAccessToken` documents the existing Go behavior of sending a bearer access token (direct token or configured token getter). The spec does not invent an OAuth grant flow: the sanitized HTTP recordings do not include OAuth exchanges. The plural `/api/v1/clap/tickets` GET remains a captured operation on the US Solutions host. The singular `/api/v1/clap/ticket/request/signalsocket` POST remains separately documented from existing Go code and local tests; the two routes have not been shown to be equivalent.

Run `python tools/verify_reference.py` to execute the Python baseline, recording adapters, HTTP/AsyncAPI body validation, and capture fixture tests.
