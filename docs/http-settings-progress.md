# Captured HTTP settings and siren support

The additive typed API covers the fields and calls backed by the checked-in C1 HTTP recordings:

| Go API | Wire contract | Evidence and boundaries |
|---|---|---|
| `GetDeviceSettings` | `GET /devices/v1/devices/{id}/settings`; returns `motion_detection_enabled` from `motion_settings` | [`device-settings-get.json`](../test/recordings/http/device-settings-get.json). The response contains many other settings; this API deliberately does not surface an unclassified raw map. |
| `PatchDeviceSettings` | `PATCH /devices/v1/devices/{id}/settings` with `{"motion_settings":{"motion_detection_enabled":bool}}` | [`device-settings-patch.json`](../test/recordings/http/device-settings-patch.json). Nil means no supported field was supplied and is rejected. Other settings are not rewritten. |
| `SetSiren` | `PUT /clients_api/doorbots/{id}/siren_on` or `_off`, no request body or query | [`siren-on.json`](../test/recordings/http/siren-on.json), [`siren-off.json`](../test/recordings/http/siren-off.json). The on response reports a duration of 30 seconds, but the captured request has no duration parameter; callers cannot set one. |

[`client_recorded_settings_test.go`](../test/system/client_recorded_settings_test.go) runs all four exchanges through the actual public `ring.Client` with the strict local replay transport, across US, EU, and FE region selections. The tests configure an explicit API origin for each region because the captures establish `api.ring.com`; they do not establish regional API hostnames. They also cover option-order independence, required captured headers, invalid IDs, an empty typed patch, and a forbidden response.

These are additions alongside the existing legacy `SetMotionDetection` method, which still uses its prior `PUT /clients_api/ring_devices/{id}/motion_detection` route. The route and body are different from the captured C1 settings operation and are retained for compatibility. The Python package's motion helper is likewise a legacy route; Python replay adapters and route comparisons are tracked in [`porting-progress.md`](porting-progress.md). No broader parity claim is made for settings omitted from the classified captures.
