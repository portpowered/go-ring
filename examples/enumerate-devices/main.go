package main

import (
	"context"
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
	accessToken := os.Getenv("RING_ACCESS_TOKEN")
	refreshToken := os.Getenv("RING_REFRESH_TOKEN")

	if accessToken == "" && refreshToken == "" {
		return ringapimodels.NewBadRequestError(
			"Either RING_ACCESS_TOKEN or RING_REFRESH_TOKEN environment variable must be set",
			nil,
		)
	}

	var (
		client *ring.Client
		err    error
	)

	// Create client with access token if available, otherwise use refresh token

	if accessToken != "" {
		fmt.Println("Creating client with access token...")

		client, err = ring.NewClient()
		if err != nil {
			return exampleerrors.Wrap("create Ring client", err)
		}
	} else {
		fmt.Println("Creating client to refresh token...")

		client, err = ring.NewClient()
		if err != nil {
			return exampleerrors.Wrap("create Ring client", err)
		}

		// Refresh token to get access token
		authResp, err := client.RefreshToken(ctx, ring.RefreshTokenRequest{
			RefreshToken: refreshToken,
			HardwareID:   "",
		})
		if err != nil {
			return exampleerrors.Wrap("refresh Ring access token", err)
		}

		fmt.Printf("✓ Token refreshed successfully (expires in %d seconds)\n\n", authResp.ExpiresIn)
		accessToken = authResp.AccessToken
	}

	auth := ring.AuthContext{AccessToken: accessToken, HardwareID: ""}

	// List all devices
	fmt.Println("Enumerating devices...")

	devices, err := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: auth})
	if err != nil {
		return exampleerrors.Wrap("list Ring devices", err)
	}

	if devices == nil {
		return ringapimodels.NewConnectionError("Devices response is nil", nil)
	}

	// Display devices without assuming a hardware family implies support.
	totalDevices := len(devices.Devices)
	fmt.Printf("✓ Found %d total device(s)\n", totalDevices)
	fmt.Println()

	for i, device := range devices.Devices {
		fmt.Printf("  %d. %s (ID: %s, kind: %s, family: %s)\n", i+1, device.Name, device.ID, device.Kind, device.Family)
		fmt.Printf("     Capabilities: %v\n", device.Capabilities)

		if device.Health != nil && device.Health.BatteryLevel != nil {
			fmt.Printf("     Battery: %d%%\n", *device.Health.BatteryLevel)
		}
	}

	if totalDevices == 0 {
		fmt.Println("No devices found. This may be expected if no devices are registered to the account.")
	} else {
		fmt.Println("Example completed successfully!")
	}

	return nil
}
