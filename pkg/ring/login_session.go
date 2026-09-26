package ring

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/pkg/dependencies/rest"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

// LoginSession owns one customer's PKCE challenge and cookies. Separate login
// sessions may run concurrently on the same Client.
type LoginSession struct {
	mu         sync.Mutex
	restClient *rest.Client
	username   string
	password   string
	hardwareID string
	done       bool
}

type LoginSessionRequest struct {
	Username   string
	Password   string
	HardwareID string
}

type CompleteLoginRequest struct{ OTPCode string }

func (c *Client) NewLoginSession(req LoginSessionRequest) (*LoginSession, error) {
	if req.Username == "" || req.Password == "" {
		return nil, ringapimodels.NewBadRequestError("username and password are required", nil)
	}
	hardwareID := req.HardwareID
	if hardwareID == "" {
		hardwareID = uuid.NewString()
	}
	// OAuth flow cookies belong to the login, not to the shared HTTP client.
	httpClient := *c.restClient.HTTPClient()
	httpClient.Jar = nil
	return &LoginSession{
		restClient: rest.NewClient(
			rest.WithHTTPClient(&httpClient),
			rest.WithEndpointBases(c.endpoints.APIBaseURL, c.endpoints.OAuthBaseURL),
			rest.WithUserAgent(c.userAgent),
			rest.WithHardwareID(hardwareID),
		),
		username: req.Username, password: req.Password, hardwareID: hardwareID,
	}, nil
}

func (s *LoginSession) HardwareID() string { return s.hardwareID }

func (s *LoginSession) Request2FACode(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return ringapimodels.NewClosedError("login session is closed")
	}
	return s.restClient.Request2FACode(ctx, s.username, s.password, s.hardwareID)
}

func (s *LoginSession) Authenticate(ctx context.Context, req CompleteLoginRequest) (*ringapimodels.AuthResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return nil, ringapimodels.NewClosedError("login session is closed")
	}
	tokens, err := s.restClient.Authenticate(ctx, s.username, s.password, s.hardwareID, req.OTPCode)
	if err != nil {
		return nil, err
	}
	s.done = true
	s.password = ""
	return &ringapimodels.AuthResponse{
		AccessToken: tokens.AccessToken, RefreshToken: tokenString(tokens.RefreshToken),
		ExpiresIn: tokenInt(tokens.ExpiresIn), TokenType: tokens.TokenType,
	}, nil
}

func (s *LoginSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	s.password = ""
	s.restClient = nil
	return nil
}
