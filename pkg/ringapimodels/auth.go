package ringapimodels

// AuthResponse represents an authentication response
type AuthResponse struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	TokenType    string
}
