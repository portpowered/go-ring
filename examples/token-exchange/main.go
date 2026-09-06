package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/portpowered/go-ring/pkg/ring"
)

func main() {
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
		log.Fatal("RING_USERNAME and RING_PASSWORD environment variables must be set")
	}

	// Create a new Ring client
	client, err := ring.NewClient()
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Step 1: Authenticate with username/password to get access and refresh tokens
	fmt.Println("Step 1: Authenticating with username/password...")
	var otpCode string

	if ringOtpCode == "" {

		err = client.Request2FACode(ctx, ring.Request2FACodeRequest{
			Username: username,
			Password: password,
		})
		if err != nil {
			log.Fatalf("Failed to request 2FA code: %v", err)
		}
		fmt.Println("✓ 2FA code requested. Please check your email or authenticator app for the code.")
		fmt.Print("Enter 2FA code: ")

		_, err = fmt.Scanln(&otpCode)
		if err != nil {
			log.Fatalf("Failed to read 2FA code: %v", err)
		}

		if otpCode == "" {
			log.Fatal("2FA code cannot be empty")
		}
	} else {
		otpCode = ringOtpCode
	}

	fmt.Println("Authenticating with 2FA code...")
	authResp, err := client.Authenticate(ctx, ring.AuthenticateRequest{
		Username: username,
		Password: password,
		OTPCode:  otpCode,
	})
	if err != nil {
		log.Fatalf("Failed to authenticate with 2FA code: %v", err)
	}

	if authResp == nil {
		log.Fatal("Authentication response is nil")
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
		log.Fatalf("Failed to encode tokens: %v", err)
	}
	if err := os.WriteFile(tokenFile, append(encoded, '\n'), 0o600); err != nil {
		log.Fatalf("Failed to write tokens to %s: %v", tokenFile, err)
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
}
