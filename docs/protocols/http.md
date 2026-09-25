# HTTP SDK contract and evidence

[`api/openapi.yaml`](../../api/openapi.yaml) brings together sanitized C1 captures, existing Go SDK operations, and pinned Python behavior. Portable synthetic fixtures identify the legacy control requests shared by Python and Go; they are marked separately from captured C1 operations. An operation marked `existing-go` or `pinned-python-and-go-synthetic-replay` is not proof that Ring currently accepts that request. Captured routes such as EVM timeline/history, v3 settings, and siren remain separate from similarly named legacy SDK routes.

## Authentication and client session

Normal API calls use `Authorization: Bearer` from the configured access token or token getter. OAuth authorization and credential requests are a separate cookie-backed PKCE flow on `oauth.ring.com`; the existing client also retains an explicitly callable token refresh and a legacy password-grant fallback when the authorization endpoint is unavailable. Credentials and verification codes are sent as form values over HTTPS. The spec describes source-level request shapes only: no C1 capture contains OAuth token exchange. See [authentication behavior](../auth.md).

When a hardware ID is configured or recovered from a valid access-token claim, the client registers `/clients_api/session` before inventory and signaling. The body schema reflects `RegisterSession` in the Go source. Session registration is not part of the captured recording set.

## Controls and known route differences

| Go operation | Go request shape | Python comparison | C1 status |
|---|---|---|---|
| `SetVolume` | Family-specific `PUT /clients_api/{chimes,doorbots}/{id}` with description and setting in query values | Matches Python's shared legacy replay fixtures; caller supplies kind and description | Synthetic Python fixture; no C1 volume-request capture |
| `SetLights` | `PUT /clients_api/doorbots/{id}/floodlight_light_{on,off}`, no body | Matches Python's legacy control shape; duration is rejected | Synthetic Python on fixture; no C1 light capture |
| `SetMotionDetection` | `PATCH /devices/v1/devices/{id}/settings` with nested `motion_settings` | Matches Python and the typed Go settings method | C1 settings PATCH and shared replay fixture |
| `TestSound` | `POST /clients_api/chimes/{id}/play_sound?kind=...`, no body | Matches Python's legacy control shape | Synthetic Python fixture; no C1 sound capture |
| `SetInHomeChime` | `PUT /clients_api/doorbots/{id}` with description and one chime field in query values | Matches Python's type, enabled, or duration request shapes | Synthetic Python fixtures; no matching C1 chime-field request |
| `UpdateDeviceHealth` | `GET /clients_api/ring_devices/{id}/health` | Python uses family-specific doorbot or chime health routes | No matching capture; current route is not asserted equivalent |

The control methods above replaced earlier generic `/clients_api/ring_devices/{id}` commands that lacked supporting wire evidence. Local fixture success alone does not establish vendor compatibility. Captured settings and siren operations also have explicit recording-backed coverage.

## History and media

`GetDeviceHistory` calls the legacy doorbot history path and decodes a direct JSON array. Go exposes optional `limit` and `kind`; Python additionally supports pagination via `older_than` and client-side retries/enforcement. C1 instead records `/evm/v3/history/devices` and `/evm/v2/timeline/devices/{id}` with distinct envelopes and query names. The OpenAPI document preserves these as separate contracts.

`GetActiveDings` uses `/clients_api/dings/active`; the path is also present in Python, but it has no matching checked-in C1 response. `GetRecording` requests `/clients_api/dings/{id}/recording` with `Accept: video/mp4` and returns a live response body. The caller must close that body. It does not buffer the recording or save a file. The default Go `http.Client` follows redirects according to its redirect policy; a custom client can change that behavior. No sanitized response establishes a redirect target, media host, or signed URL shape.

## Error boundaries

Client-side request validation can return `BadRequestError` before any HTTP request. Token-provider failures return `TokenError`; OAuth responses can map to `AuthenticationError`, `Requires2FAError`, or `RateLimitError`. Non-success API responses are represented as `HTTPError`, while transport failures are `NetworkError` or `ConnectionError`. These are existing Go error categories, not a claim that each status/body shape has been observed for every operation. Captured successful response schemas remain authoritative where available; generic error response schemas are intentionally non-specific.
