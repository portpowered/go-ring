package main

import (
	"context"
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
	refreshToken := os.Getenv("RING_REFRESH_TOKEN")
	ringOtpCode := os.Getenv("RING_OTP_CODE")
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
	fmt.Printf("  Access Token: %s...%s (length: %d)\n",
		authResp.AccessToken[:20],
		authResp.AccessToken[len(authResp.AccessToken)-10:],
		len(authResp.AccessToken))
	fmt.Printf("  Refresh Token: %s...%s (length: %d)\n",
		authResp.RefreshToken[:20],
		authResp.RefreshToken[len(authResp.RefreshToken)-10:],
		len(authResp.RefreshToken))
	fmt.Printf("  Token Type: %s\n", authResp.TokenType)
	fmt.Printf("  Expires In: %d seconds\n", authResp.ExpiresIn)
	fmt.Println()

	// Step 2: Use the refresh token to get a new access token
	refreshToken = authResp.RefreshToken
	fmt.Println("Step 2: Refreshing token using refresh token from environment...")
	refreshResp, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
		RefreshToken: refreshToken,
	})
	if err != nil {
		log.Fatalf("Failed to refresh token: %v", err)
	}

	if refreshResp == nil {
		log.Fatal("Token refresh response is nil")
	}

	fmt.Printf("✓ Token refresh successful!\n")
	fmt.Printf("  New Access Token: %s...%s (length: %d)\n",
		refreshResp.AccessToken[:20],
		refreshResp.AccessToken[len(refreshResp.AccessToken)-10:],
		len(refreshResp.AccessToken))
	if refreshResp.RefreshToken != "" {
		fmt.Printf("  New Refresh Token: %s...%s (length: %d)\n",
			refreshResp.RefreshToken[:20],
			refreshResp.RefreshToken[len(refreshResp.RefreshToken)-10:],
			len(refreshResp.RefreshToken))
	}
	fmt.Printf("  Token Type: %s\n", refreshResp.TokenType)
	fmt.Printf("  Expires In: %d seconds\n", refreshResp.ExpiresIn)
	fmt.Println()

	fmt.Println("Example completed successfully!")
}
