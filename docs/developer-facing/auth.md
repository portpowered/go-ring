# Ring authentication

`go-ring` authenticates server-side with Ring's OAuth 2.0 authorization-code flow and PKCE. Ring requires two-factor authentication for most accounts, so initial authentication is a two-call operation: request a code, then complete the same pending OAuth session with that code.

Ring's APIs are unofficial and may change without notice. The implementation follows the current Android-client-compatible flow and retains a legacy password-grant fallback only when the OAuth v2 authorization endpoint is unavailable.

## CORS:
1. None of the endpoints support CORS. if you want to perform auth, you need a backend server to do it for you.
## Endpoints

- Authorization: `https://oauth.ring.com/oauth/v2/authorize`
- Credential submission: `https://oauth.ring.com/oauth/v2/signin`
- 2FA verification: `https://oauth.ring.com/oauth/v2/2fa/verify`
- Code exchange and refresh: `https://oauth.ring.com/oauth/token`
- Client session registration: `https://api.ring.com/clients_api/session`
- Device inventory: `https://api.ring.com/device_info/v3/devices`

All calls must be made from a backend process. Ring's OAuth pages do not support cross-origin browser authentication.

The client has no credential options or token fallback. Each account operation
requires an `AuthContext` in its request. `RefreshToken` requires an explicit
refresh token and hardware ID when one is available.

## Concurrent authentication

Use `client.NewLoginSession` for each account when one client serves multiple
users. Each login session owns its PKCE verifier, OAuth state, cookies, and
hardware ID. `Request2FACode` and `Authenticate` on that session do not change
the shared client. Close the session after saving its returned tokens.

```go
flow, err := client.NewLoginSession(ring.LoginSessionRequest{
    Username: username, Password: password, HardwareID: hardwareID,
})
if err != nil { return err }
defer flow.Close()
if err := flow.Request2FACode(ctx); err != nil { return err }
tokens, err := flow.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otpCode})
```

If `HardwareID` is omitted, the session generates one; save
`flow.HardwareID()` with the tokens. Supplying a refresh token in
`RefreshTokenRequest` returns rotated tokens without changing the shared
client. Pass `HardwareID` in that request for the matching account.

## Initial authentication

Use one `LoginSession` for both steps of a 2FA exchange. The client-level
`Authenticate` and `Request2FACode` methods each start an independent exchange;
they do not retain a challenge or bind returned credentials to the client.

The login session performs these steps:

1. Generate a cryptographically random PKCE verifier, S256 challenge, OAuth state, and persistent hardware UUID.
2. Open `/oauth/v2/authorize` and retain Ring's cookies and CSRF token.
3. Submit credentials to `/oauth/v2/signin`, which normally triggers 2FA.
4. Submit the code to `/oauth/v2/2fa/verify` using the same cookies and CSRF token.
5. Follow the authorization redirect, validate its state, and exchange the returned code with the PKCE verifier.
6. Rotate the returned refresh token once. Ring's client APIs may reject the initial code-exchange access token, while the rotated access token is immediately usable.

Do not call `Request2FACode` repeatedly. Ring rate-limits verification-code delivery. Keep the same login session until the challenge is completed.

## Token response and storage

Authentication and refresh return the same structure:

```json
{
  "access_token": "...",
  "refresh_token": "...",
  "expires_in": 14400,
  "token_type": "Bearer"
}
```

- Access tokens normally last four hours.
- Refresh tokens are rotated. Persist the complete response after every successful authentication or refresh; continuing to store the previous refresh token can force another 2FA login.
- Store token files with owner-only permissions such as `0600` and never log token contents.
- The access-token JWT may include the hardware ID. Each request's `AuthContext` recovers it when the token has a decodable claim; malformed or missing claims are ignored. An explicit `AuthContext.HardwareID` takes precedence and determines that request's session registration identity.

The token-exchange example writes the complete response without printing either token:

```bash
RING_USERNAME='user@example.com' \
RING_PASSWORD='...' \
RING_TOKEN_FILE="$HOME/.agent-cli/secrets/token.json" \
go run ./examples/token-exchange
```

## Using stored tokens

Pass the current access token in every account-level request. The client does
not store access or refresh tokens, usernames, passwords, or hardware IDs:

```go
client, err := ring.NewClient()
if err != nil {
	return err
}
defer client.Close()

auth := ring.AuthContext{AccessToken: tokens.AccessToken, HardwareID: hardwareID}
devices, err := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: auth})
```

Before device discovery or RTC signaling for that request, the client:

1. Uses the supplied hardware ID, or recovers it from the token's claim.
2. Registers `/clients_api/session` for a request with a hardware ID. The
   shared client never uses one account's registration for another account.
3. Fetches inventory from `/device_info/v3/devices` and maps the flat response into the library's existing doorbell, chime, camera, and other-device collections.

Applications that explicitly call `RefreshToken` must persist the returned `AuthResponse`, including its rotated refresh token.

## Refresh flow

```go
client, err := ring.NewClient()
if err != nil {
	return err
}

tokens, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
	RefreshToken: stored.RefreshToken,
	HardwareID:   hardwareID,
})
if err != nil {
	return err
}

// Atomically replace the stored token response here.
```

The refresh request uses `grant_type=refresh_token`, `client_id=ring_official_android`, `scope=client`, the Android user agent, and the persistent hardware ID when available.

## Error handling

- `Requires2FAError`: a verification code was sent and must be submitted through the same login session.
- `AuthenticationError`: credentials, verification code, authorization state, or token were rejected.
- `RateLimitError`: Ring rate-limited authentication or code delivery.
- `TokenError`: no usable token exists or an expired token could not be refreshed.
- `ConnectionError`: session registration or RTC signaling failed.

Treat all authentication errors as sensitive. Response bodies may contain account or session details and should not be written to public logs.

## Security guidance

- Keep username, password, OTP codes, access tokens, and refresh tokens out of source control and logs.
- Prefer refresh-token authentication after the initial 2FA flow.
- Persist each rotated refresh token before discarding the prior token.
- Use one stable hardware ID per account installation; do not share it across customer accounts.
- Use HTTPS exclusively.
- Restrict token-file permissions and encrypt secrets at rest where practical.
- Never implement this flow directly in frontend JavaScript; use a trusted backend.
