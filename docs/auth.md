# Ring authentication

`go-ring` authenticates server-side with Ring's OAuth 2.0 authorization-code flow and PKCE. Ring requires two-factor authentication for most accounts, so initial authentication is a two-call operation: request a code, then complete the same pending OAuth session with that code.

Ring's APIs are unofficial and may change without notice. The implementation follows the current Android-client-compatible flow and retains a legacy password-grant fallback only when the OAuth v2 authorization endpoint is unavailable.

## Endpoints

- Authorization: `https://oauth.ring.com/oauth/v2/authorize`
- Credential submission: `https://oauth.ring.com/oauth/v2/signin`
- 2FA verification: `https://oauth.ring.com/oauth/v2/2fa/verify`
- Code exchange and refresh: `https://oauth.ring.com/oauth/token`
- Client session registration: `https://api.ring.com/clients_api/session`
- Device inventory: `https://api.ring.com/device_info/v3/devices`

All calls must be made from a backend process. Ring's OAuth pages do not support cross-origin browser authentication.

## Initial authentication

Create one client and use it for both 2FA calls. The client retains the PKCE verifier, OAuth state, CSRF token, cookies, and hardware ID between calls.

```go
client, err := ring.NewClient()
if err != nil {
	return err
}
defer client.Close()

err = client.Request2FACode(ctx, ring.Request2FACodeRequest{
	Username: username,
	Password: password,
})
if err != nil {
	return err
}

tokens, err := client.Authenticate(ctx, ring.AuthenticateRequest{
	Username: username,
	Password: password,
	OTPCode:  otpCode,
})
if err != nil {
	return err
}
```

The flow performs these steps:

1. Generate a cryptographically random PKCE verifier, S256 challenge, OAuth state, and persistent hardware UUID.
2. Open `/oauth/v2/authorize` and retain Ring's cookies and CSRF token.
3. Submit credentials to `/oauth/v2/signin`, which normally triggers 2FA.
4. Submit the code to `/oauth/v2/2fa/verify` using the same cookies and CSRF token.
5. Follow the authorization redirect, validate its state, and exchange the returned code with the PKCE verifier.
6. Rotate the returned refresh token once. Ring's client APIs may reject the initial code-exchange access token, while the rotated access token is immediately usable.

Do not call `Request2FACode` repeatedly. Ring rate-limits verification-code delivery, and starting a new client discards the pending OAuth session needed to verify the code.

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
- The access-token JWT includes the hardware ID. `NewClientWithToken` recovers it automatically so subsequent session registration uses the same identity.

The token-exchange example writes the complete response without printing either token:

```bash
RING_USERNAME='user@example.com' \
RING_PASSWORD='...' \
RING_TOKEN_FILE="$HOME/.agent-cli/secrets/token.json" \
go run ./examples/token-exchange
```

## Using stored tokens

Load the current access token when constructing the client:

```go
client, err := ring.NewClientWithToken(tokens.AccessToken)
if err != nil {
	return err
}
defer client.Close()

devices, err := client.ListDevices(ctx)
```

Before device discovery or RTC signaling, the client:

1. Recovers and reuses the token's hardware ID.
2. Registers `/clients_api/session` once per client instance.
3. Fetches inventory from `/device_info/v3/devices` and maps the flat response into the library's existing doorbell, chime, camera, and other-device collections.

Applications that explicitly call `RefreshToken` must persist the returned `AuthResponse`, including its rotated refresh token.

## Refresh flow

```go
client, err := ring.NewClientWithToken(stored.AccessToken)
if err != nil {
	return err
}

tokens, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
	RefreshToken: stored.RefreshToken,
})
if err != nil {
	return err
}

// Atomically replace the stored token response here.
```

The refresh request uses `grant_type=refresh_token`, `client_id=ring_official_android`, `scope=client`, the Android user agent, and the persistent hardware ID when available.

## Error handling

- `Requires2FAError`: a verification code was sent and must be submitted through the same client.
- `AuthenticationError`: credentials, verification code, authorization state, or token were rejected.
- `RateLimitError`: Ring rate-limited authentication or code delivery.
- `TokenError`: no usable token exists or an expired token could not be refreshed.
- `ConnectionError`: session registration or RTC signaling failed.

Treat all authentication errors as sensitive. Response bodies may contain account or session details and should not be written to public logs.

## Security guidance

- Keep username, password, OTP codes, access tokens, and refresh tokens out of source control and logs.
- Prefer refresh-token authentication after the initial 2FA flow.
- Persist each rotated refresh token before discarding the prior token.
- Use one stable hardware ID per installation.
- Use HTTPS exclusively.
- Restrict token-file permissions and encrypt secrets at rest where practical.
- Never implement this flow directly in frontend JavaScript; use a trusted backend.
