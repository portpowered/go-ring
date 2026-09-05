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
	accessToken := os.Getenv("RING_ACCESS_TOKEN")
	refreshToken := os.Getenv("RING_REFRESH_TOKEN")

	if accessToken == "" && refreshToken == "" {
		log.Fatal("Either RING_ACCESS_TOKEN or RING_REFRESH_TOKEN environment variable must be set")
	}

	var client *ring.Client
	var err error

	// Create client with access token if available, otherwise use refresh token
	if accessToken != "" {
		fmt.Println("Creating client with access token...")
		client, err = ring.NewClientWithToken(accessToken)
		if err != nil {
			log.Fatalf("Failed to create client: %v", err)
		}
	} else {
		fmt.Println("Creating client to refresh token...")
		client, err = ring.NewClient()
		if err != nil {
			log.Fatalf("Failed to create client: %v", err)
		}

		// Refresh token to get access token
		authResp, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
			RefreshToken: refreshToken,
		})
		if err != nil {
			log.Fatalf("Failed to refresh token: %v", err)
		}
		fmt.Printf("✓ Token refreshed successfully (expires in %d seconds)\n\n", authResp.ExpiresIn)
	}
	defer client.Close()

	// List all devices
	fmt.Println("Enumerating devices...")
	devices, err := client.ListDevices(ctx)
	if err != nil {
		log.Fatalf("Failed to list devices: %v", err)
	}

	if devices == nil {
		log.Fatal("Devices response is nil")
	}

	// Display device counts
	totalDevices := len(devices.Doorbells) + len(devices.Chimes) + len(devices.StickUpCams) + len(devices.Other)
	fmt.Printf("✓ Found %d total device(s)\n", totalDevices)
	fmt.Printf("  - Doorbells: %d\n", len(devices.Doorbells))
	fmt.Printf("  - Chimes: %d\n", len(devices.Chimes))
	fmt.Printf("  - StickUp Cams: %d\n", len(devices.StickUpCams))
	fmt.Printf("  - Other: %d\n", len(devices.Other))
	fmt.Println()

	// Display doorbells
	if len(devices.Doorbells) > 0 {
		fmt.Println("Doorbells:")
		for i, doorbell := range devices.Doorbells {
			fmt.Printf("  %d. Name: %s\n", i+1, doorbell.Name)
			fmt.Printf("     ID: %s\n", doorbell.ID)
			fmt.Printf("     Address: %s\n", doorbell.Address)
			fmt.Printf("     Family: %s\n", doorbell.Family)
			if doorbell.Health != nil && doorbell.Health.BatteryLevel != nil {
				fmt.Printf("     Battery: %d%%\n", *doorbell.Health.BatteryLevel)
			}
			fmt.Println()
		}
	}

	// Display chimes
	if len(devices.Chimes) > 0 {
		fmt.Println("Chimes:")
		for i, chime := range devices.Chimes {
			fmt.Printf("  %d. Name: %s\n", i+1, chime.Name)
			fmt.Printf("     ID: %s\n", chime.ID)
			fmt.Printf("     Address: %s\n", chime.Address)
			fmt.Printf("     Family: %s\n", chime.Family)
			fmt.Println()
		}
	}

	// Display stickup cams
	if len(devices.StickUpCams) > 0 {
		fmt.Println("StickUp Cams:")
		for i, cam := range devices.StickUpCams {
			fmt.Printf("  %d. Name: %s\n", i+1, cam.Name)
			fmt.Printf("     ID: %s\n", cam.ID)
			fmt.Printf("     Description: %s\n", cam.Description)
			fmt.Printf("     Address: %s\n", cam.Address)
			fmt.Printf("     Family: %s\n", cam.Family)
			if cam.Health != nil && cam.Health.BatteryLevel != nil {
				fmt.Printf("     Battery: %d%%\n", *cam.Health.BatteryLevel)
			}
			fmt.Println()
		}
	}

	// Display other devices
	if len(devices.Other) > 0 {
		fmt.Println("Other Devices:")
		for i, other := range devices.Other {
			fmt.Printf("  %d. Name: %s\n", i+1, other.Name)
			fmt.Printf("     ID: %s\n", other.ID)
			fmt.Printf("     Kind: %s\n", other.Kind)
			fmt.Printf("     Family: %s\n", other.Family)
			fmt.Println()
		}
	}

	if totalDevices == 0 {
		fmt.Println("No devices found. This may be expected if no devices are registered to the account.")
	} else {
		fmt.Println("Example completed successfully!")
	}
}
