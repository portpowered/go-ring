// Package protocol contains Ring wire endpoint defaults used by the client.
package protocol

const (
	OAuthBaseURL                  = "https://oauth.ring.com"
	APIBaseURL                    = "https://api.ring.com"
	USSolutionsBaseURL            = "https://prd-api-us.prd.rings.solutions"
	SignalingURL                  = "wss://api.prod.signalling.ring.devices.a2z.com:443/ws?api_version=4.0&auth_type=ring_solutions&client_id=ring_site-{client_id}&token={token}"
	ExperimentalEventWebSocketURL = "wss://api.ring.com/clients_api/ws"
	OAuthTokenPath                = "/oauth/token"
	OAuthAuthorizePath            = "/oauth/v2/authorize"
	OAuthSigninPath               = "/oauth/v2/signin"
	OAuthCallbackURL              = "https://ring.com/signin/callback"
	TicketPath                    = "/api/v1/clap/ticket/request/signalsocket"
	DevicesPath                   = "/clients_api/ring_devices"
	DevicesV3Path                 = "/device_info/v3/devices"
	SessionPath                   = "/clients_api/session"
	DingsActivePath               = "/clients_api/dings/active"
	DingsHistoryPath              = "/clients_api/dings/history"
	RecordingPath                 = "/clients_api/dings/{id}/recording"
)
