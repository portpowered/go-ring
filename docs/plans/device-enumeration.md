# Device enumeration and capabilities

- `Client.ListDevices` returns `DevicesResponse{Devices: []Device}` in inventory order. `Client.GetDevice` returns one `*Device` found by ID.
- Every device has the same public model: ID, name, open hardware kind, open server family, optional metadata, health, and capabilities. The client does not classify or drop unfamiliar hardware.
- `Device.Capabilities` lists operations confirmed by explicit inventory evidence. `Device.Supports` checks that list. A missing capability means unknown support, not proof that the operation will fail.
- `has_light: true` confirms `light`. The presence of `motion_detection_enabled` confirms `motion_detection`, even when the value is false. The presence of `health.siren_on` confirms `siren`; its value is current state, not support. `health.vod_enabled: true` confirms `live_view`.
- Each observed `health.supported_rpc_commands` PTZ command confirms only its corresponding pan or tilt, step or continuous capability. Unknown commands remain ignored until modeled.
- Inventory does not establish snapshot, reboot, intercom unlock, test sound, recording, or WebRTC setup support for every device. Those operations remain callable by ID and report their typed server result.
- The captured v3 camera list proves live view and four PTZ command capabilities. Synthetic replay cases cover absent fields, explicit false state, positive evidence, unknown kinds, and unknown RPC commands.
- The public model comes from `api/client-models.openapi.yaml`; HTTP wire models remain in `api/openapi.yaml`. Regenerate models before committing schema changes.
- This is a breaking API change: callers iterate `response.Devices` rather than family-specific slices. `GetDevice` returns a pointer to that same model. Existing device IDs and request auth remain unchanged.
