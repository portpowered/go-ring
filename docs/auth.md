This document describes the authentication system for the Ring API, including endpoints, authentication flows, CORS limitations, and token management.

## Authentication Endpoints

### Primary Endpoint
- **OAuth Token**: `https://oauth.ring.com/oauth/token`
  - Method: `POST`
  - Content-Type: `application/x-www-form-urlencoded`

## Authentication Flow

### Initial Authentication Request

The Ring OAuth flow uses a password grant type with the following parameters:

**Request Body:**
- `grant_type`: `"password"`
- `username`: User's Ring email address
- `password`: User's Ring password
- `client_id`: `"ring_official_android"`
- `scope`: `"client"`

**Required Headers:**
- `Content-Type`: `application/x-www-form-urlencoded`
- `User-Agent`: `android:com.ringapp`
- `hardware_id`: UUID string (should be generated once and reused per device/installation)

### Two-Factor Authentication (2FA)

Ring requires 2FA for most accounts. The authentication flow handles this as follows:

1. **Initial Request** (without 2FA code)
   - Submit username and password
   - Server responds with HTTP `412 Precondition Failed` if 2FA is required
   - Response body may include:
     ```json
     {
       "next_time_in_secs": 60,
       "phone": "+1xxxxxxxx67",
       "tsv_state": "sms"
     }
     ```
     This denotes that an OTP code is required and its been sent to the user's phone number for the next 60 seconds.

2. **2FA Verification Request** (with OTP code)
   - Include additional headers:
     - `2fa-support`: `"true"`
     - `2fa-code`: The 6-digit OTP code from SMS/authenticator app
   - Resubmit with same credentials plus OTP code
   - Server responds with tokens on success

### Successful Authentication Response

On successful authentication (with or without 2FA), the API returns:

```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_in": 14400,
  "token_type": "Bearer",
  "scope": "client"
}
```

**Token Details:**
- `access_token`: JWT token for authenticated API requests
- `refresh_token`: JWT token for refreshing the access token
- `expires_in`: Token validity period in seconds (typically 14400 = 4 hours)
- `token_type`: Always `"Bearer"`
- `scope`: Granted scopes (typically `"client"`)

### JWT Claims (Access/Refresh Tokens)

Both the access and refresh tokens are JWTs with the following payload shape (example):

#### Access Token JWT Claims
```json
{
  "app_id": "ring_official_android",
  "cid": "ring_official_android",
  "exp": 1766497048,
  "hardware_id": "12312312312-eac7-4bdf-85eb-aba3fa64f681",
  "iat": 1766482648,
  "iss": "RingOauthService-prod:us-east-1:cef51789",
  "oiat": 1766482648,
  "rnd": "123123123",
  "scopes": ["client"],
  "session_id": "ring-session-123123123-b2c2-4261-b92a-8ba2b5d8311a",
  "user_id": 180752665
}
```

#### Refresh Token JWT Claims
```json
{
  "iat": 1766482648,
  "iss": "RingOauthService-prod:us-east-1:cef51789",
  "oiat": 1766482648,
  "refresh_cid": "ring_official_android",
  "refresh_scopes": [
    "client"
  ],
  "refresh_user_id": 180752665,
  "rnd": "12312312",
  "session_id": "ring-session-123123123b-b2c2-4261-b92a-8ba2b5d8311a",
  "type": "refresh-token"
}
```

Field notes:
- `app_id` / `cid`: Client identifier (`ring_official_android` for the mobile app).
- `hardware_id`: The hardware UUID supplied in the request; persists across sessions.
- `session_id`: Server-generated session identifier.
- `scopes`: Granted scopes (typically `client`).
- `exp`: Expiry epoch seconds (access token ~4 hours).
- `iat` / `oiat`: Issued-at timestamps.
- `iss`: Ring OAuth issuer identifier.
- `user_id`: Internal Ring user identifier.
- `rnd`: Random nonce value returned by Ring.

## CORS Limitations

### Critical Limitation

**The Ring OAuth endpoint does NOT have CORS enabled.** This means:

- ❌ Direct browser-based `fetch()` calls will fail with CORS errors
- ❌ Browser preflight (OPTIONS) requests receive no CORS headers
- ✅ Server-side requests work correctly (no CORS restrictions)

### CORS Test Results

When testing the endpoint:
- **OPTIONS request**: Returns `400 Bad Request` with no CORS headers
- **GET/POST from browser**: Fails with "Failed to fetch" (CORS error)
- **Server-side requests**: Work correctly via curl or backend services

### Required Solution

**A backend proxy service is required** to handle Ring authentication:

1. Frontend sends credentials to backend endpoint
2. Backend makes request to `https://oauth.ring.com/oauth/token`
3. Backend returns tokens to frontend
4. Frontend uses tokens for subsequent API calls

This is a common pattern for OAuth providers that don't support CORS.

## Required HTTP Headers

### Request Headers (All Requests)
- `Content-Type`: `application/x-www-form-urlencoded`
- `User-Agent`: `android:com.ringapp` (must match Android app user agent)
- `hardware_id`: UUID string (should be persistent per installation)

### Request Headers (2FA Requests Only)
- `2fa-support`: `"true"`
- `2fa-code`: 6-digit OTP code (e.g., `"965007"`)

### Response Headers
- Standard HTTP headers only
- No custom CORS headers present
- Tokens are in response body, not headers

## Hardware ID Management

The `hardware_id` header is used to identify the device/client:

- **Format**: UUID v4 string (e.g., `"7198882a-eac7-4bdf-85eb-aba3fa64f681"`)
- **Generation**: Should be generated once per installation/device
- **Persistence**: Should be stored and reused across authentication sessions
- **Purpose**: Helps Ring track and manage device sessions

**Implementation Note**: In production, generate a hardware ID once and store it securely. Reusing the same hardware ID helps maintain session continuity.

## Error Handling

### HTTP Status Codes

- **200 OK**: Authentication successful, tokens returned
- **412 Precondition Failed**: 2FA required (initial request without OTP)
- **400 Bad Request**: Invalid request parameters or credentials
- **401 Unauthorized**: Invalid credentials (username/password incorrect)

### Error Response Format

```json
{
  "error": "invalid_request",
  "error_description": "The request is missing a required parameter, includes an invalid parameter value, includes a parameter more than once, or is otherwise malformed."
}
```

### 2FA Error Response

When 2FA is required, the response may include:

```json
{
  "next_time_in_secs": 60,
  "phone": "+1xxxxxxxx67",
  "tsv_state": "sms"
}
```

This indicates:
- `next_time_in_secs`: Time until next OTP can be requested
- `phone`: Masked phone number where OTP was sent
- `tsv_state`: Type of 2FA (`"sms"` for SMS-based)

## Token Management

### Access Token
- **Type**: JWT (JSON Web Token)
- **Validity**: Typically 4 hours (14400 seconds)
- **Usage**: Include in `Authorization: Bearer {access_token}` header for API requests
- **Refresh**: Use refresh token to obtain new access token when expired

### Refresh Token
- **Type**: JWT (JSON Web Token)
- **Validity**: Longer-lived than access token
- **Usage**: Exchange for new access token when current one expires
- **Storage**: Must be stored securely (encrypted at rest recommended)

### Token Refresh Flow

When access token expires:
1. Use refresh token to obtain new access token
2. Refresh endpoint: `https://oauth.ring.com/oauth/token`
3. Request body:
   - `grant_type`: `"refresh_token"`
   - `refresh_token`: Current refresh token
   - `client_id`: `"ring_official_android"`

## Security Considerations

### Credential Security
- **Never store passwords**: Only store tokens after successful authentication
- **Encrypt tokens at rest**: Access and refresh tokens should be encrypted when stored
- **Secure transmission**: Always use HTTPS for all authentication requests
- **Token rotation**: Implement refresh token rotation when possible

### CORS Security
- The lack of CORS is actually a security feature for Ring
- Forces authentication through backend services
- Prevents direct credential exposure in frontend code
- Backend can implement additional security measures (rate limiting, logging, etc.)

### Hardware ID Security
- Hardware ID should be treated as semi-sensitive
- Can be used to track devices/sessions
- Should be generated securely (cryptographically random UUID)
- Consider device fingerprinting implications

### Two-Factor Authentication
- 2FA codes are time-sensitive (typically 60 seconds)
- Codes should be entered promptly
- Failed attempts may trigger rate limiting
- SMS-based 2FA requires phone number verification

## Implementation Notes

### Frontend Implementation (Current State)

The current frontend implementation (`RingLogin.tsx`) attempts direct browser-based authentication but will fail due to CORS:

```typescript
// This will fail in browser due to CORS
const response = await fetch(RING_OAUTH_ENDPOINT, {
  method: "POST",
  headers: {
    "Content-Type": "application/x-www-form-urlencoded",
    "User-Agent": "android:com.ringapp",
    "hardware_id": hardwareId,
  },
  body: formData.toString(),
});
```

**TODO**: Implement backend proxy endpoint to handle authentication.

### Backend Implementation (Required)

A backend endpoint should be created to proxy authentication requests:

1. **Endpoint**: `/api/integrations/ring/auth` (or similar)
2. **Method**: `POST`
3. **Request Body**:
   ```json
   {
     "username": "user@example.com",
     "password": "password",
     "otpCode": "965007" // optional, only for 2FA
   }
   ```
4. **Backend Process**:
   - Validate request
   - Make request to `https://oauth.ring.com/oauth/token`
   - Return tokens to frontend
   - Handle errors appropriately

### Production Considerations

- **Rate Limiting**: Implement rate limiting on backend proxy
- **Logging**: Log authentication attempts (without sensitive data)
- **Error Handling**: Provide user-friendly error messages
- **Session Management**: Store tokens securely and manage refresh
- **Monitoring**: Monitor authentication success/failure rates
- **Hardware ID Persistence**: Store hardware ID per user/device

## Testing

### Manual Testing with curl

**Initial Request (without 2FA):**
```bash
curl -X POST "https://oauth.ring.com/oauth/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "User-Agent: android:com.ringapp" \
  -H "hardware_id: $(uuidgen)" \
  -d "grant_type=password&username=user@example.com&password=password&client_id=ring_official_android&scope=client"
```

**2FA Request (with OTP):**
```bash
curl -X POST "https://oauth.ring.com/oauth/token" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "User-Agent: android:com.ringapp" \
  -H "hardware_id: $(uuidgen)" \
  -H "2fa-support: true" \
  -H "2fa-code: 965007" \
  -d "grant_type=password&username=user@example.com&password=password&client_id=ring_official_android&scope=client"
```

### Expected Responses

**2FA Required (412):**
```json
{"next_time_in_secs":60,"phone":"+1xxxxxxxx66","tsv_state":"sms"}
```

**Success (200):**
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_in": 14400,
  "token_type": "Bearer",
  "scope": "client"
}
```

## Best Practices

1. **Always use backend proxy**: Never attempt direct browser-based authentication
2. **Store hardware ID**: Generate once and reuse for better session continuity
3. **Handle 2FA gracefully**: Provide clear UI for OTP entry
4. **Implement token refresh**: Automatically refresh tokens before expiry
5. **Secure token storage**: Encrypt tokens at rest
6. **Error handling**: Provide user-friendly error messages
7. **Rate limiting**: Implement on backend to prevent abuse
8. **Logging**: Log authentication events (without sensitive data)
9. **Monitoring**: Track authentication success/failure rates
10. **Session management**: Properly handle token expiry and refresh


## Overview

## System Context

## Design Decisions

## Overview

TODO.

## System Context

TODO.

## Design Decisions

TODO.
