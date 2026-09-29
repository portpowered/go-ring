package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/portpowered/go-ring/examples/internal/exampleerrors"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Get credentials from environment variables
	username := os.Getenv("RING_USERNAME")
	password := os.Getenv("RING_PASSWORD")
	ringOtpCode := os.Getenv("RING_OTP_CODE")

	tokenFile := os.Getenv("RING_TOKEN_FILE")
	if tokenFile == "" {
		tokenFile = "tokens.json"
	}

	if username == "" || password == "" {
		return ringapimodels.NewBadRequestError("RING_USERNAME and RING_PASSWORD environment variables must be set", nil)
	}

	// Create a new Ring client
	client, err := ring.NewClient()
	if err != nil {
		return exampleerrors.Wrap("create Ring client", err)
	}

	login, err := client.NewLoginSession(ring.LoginSessionRequest{
		Username:   username,
		Password:   password,
		HardwareID: "",
	})
	if err != nil {
		return exampleerrors.Wrap("start Ring login session", err)
	}

	defer func() { _ = login.Close() }()

	// Step 1: Authenticate with username/password to get access and refresh tokens
	fmt.Println("Step 1: Authenticating with username/password...")

	var otpCode string

	if ringOtpCode == "" {
		err = login.Request2FACode(ctx)
		if err != nil {
			return exampleerrors.Wrap("request a 2FA code", err)
		}

		fmt.Println("✓ 2FA code requested. Please check your email or authenticator app for the code.")
		fmt.Print("Enter 2FA code: ")

		_, err = fmt.Scanln(&otpCode)
		if err != nil {
			return ringapimodels.NewBadRequestError("Failed to read 2FA code", err)
		}

		if otpCode == "" {
			return ringapimodels.NewBadRequestError("2FA code cannot be empty", nil)
		}
	} else {
		otpCode = ringOtpCode
	}

	fmt.Println("Authenticating with 2FA code...")

	authResp, err := login.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otpCode})
	if err != nil {
		return exampleerrors.Wrap("authenticate Ring login session", err)
	}

	if authResp == nil {
		return ringapimodels.NewConnectionError("Authentication response is nil", nil)
	}

	fmt.Printf("✓ Authentication successful!\n")
	fmt.Printf("  Access Token length: %d\n", len(authResp.AccessToken))
	fmt.Printf("  Refresh Token length: %d\n", len(authResp.RefreshToken))
	fmt.Printf("  Token Type: %s\n", authResp.TokenType)
	fmt.Printf("  Expires In: %d seconds\n", authResp.ExpiresIn)
	fmt.Println()

	// Save full tokens to a file instead of printing them to the terminal.
	tokenData := map[string]any{
		"access_token":  authResp.AccessToken,
		"refresh_token": authResp.RefreshToken,
		"token_type":    authResp.TokenType,
		"expires_in":    authResp.ExpiresIn,
	}

	encoded, err := json.MarshalIndent(tokenData, "", "  ")
	if err != nil {
		return ringapimodels.NewInternalServerError("Failed to encode tokens", err)
	}

	{
		err := os.WriteFile(tokenFile, append(encoded, '\n'), 0o600)
		if err != nil {
			return ringapimodels.NewInternalServerError("Failed to write tokens", err)
		}
	}

	fmt.Printf("✓ Tokens saved to %s (mode 0600)\n\n", tokenFile)

	// // Step 2: Use the refresh token to get a new access token
	// refreshToken = authResp.RefreshToken
	// fmt.Println("Step 2: Refreshing token using refresh token from environment...")
	// refreshResp, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
	// 	RefreshToken: refreshToken,
	// })
	// if err != nil {
	// 	log.Fatalf("Failed to refresh token: %v", err)
	// }

	// if refreshResp == nil {
	// 	log.Fatal("Token refresh response is nil")
	// }

	// fmt.Printf("✓ Token refresh successful!\n")
	// fmt.Printf("  New Access Token: %s...%s (length: %d)\n",
	// 	refreshResp.AccessToken,
	// 	len(refreshResp.AccessToken))
	// if refreshResp.RefreshToken != "" {
	// 	fmt.Printf("  New Refresh Token: %s...%s (length: %d)\n",
	// 		refreshResp.RefreshToken,
	// 		len(refreshResp.RefreshToken))
	// }
	// fmt.Printf("  Token Type: %s\n", refreshResp.TokenType)
	// fmt.Printf("  Expires In: %d seconds\n", refreshResp.ExpiresIn)
	// fmt.Println()

	fmt.Println("Example completed successfully!")

	return nil
}
