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

	// Get access token from environment variable
	accessToken := os.Getenv("RING_ACCESS_TOKEN")
	if accessToken == "" {
		log.Fatal("RING_ACCESS_TOKEN environment variable must be set")
	}

	// Create client with access token
	client, err := ring.NewClientWithToken(accessToken)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Step 1: List all devices
	fmt.Println("Step 1: Enumerating devices...")
	devices, err := client.ListDevices(ctx)
	if err != nil {
		log.Fatalf("Failed to list devices: %v", err)
	}

	if devices == nil {
		log.Fatal("Devices response is nil")
	}

	totalDevices := len(devices.Doorbells) + len(devices.Chimes) + len(devices.StickUpCams) + len(devices.Other)
	fmt.Printf("✓ Found %d total device(s)\n", totalDevices)
	fmt.Printf("  - Doorbells: %d\n", len(devices.Doorbells))
	fmt.Printf("  - Chimes: %d\n", len(devices.Chimes))
	fmt.Printf("  - StickUp Cams: %d\n", len(devices.StickUpCams))
	fmt.Printf("  - Other: %d\n", len(devices.Other))
	fmt.Println()

	if totalDevices == 0 {
		log.Fatal("No devices found. Cannot test chime sound.")
	}

	// Step 2: Find a chime device (prefer chimes, but can use any device for testing)
	var deviceID string
	var deviceName string
	var deviceType string

	if len(devices.Chimes) > 0 {
		// Use first chime device
		deviceID = devices.Chimes[0].ID
		deviceName = devices.Chimes[0].Name
		deviceType = "Chime"
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: %s)\n", deviceName, deviceID, deviceType)
	} else if len(devices.Doorbells) > 0 {
		// Fall back to doorbell if no chimes available
		deviceID = devices.Doorbells[0].ID
		deviceName = devices.Doorbells[0].Name
		deviceType = "Doorbell"
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: %s)\n", deviceName, deviceID, deviceType)
		fmt.Println("  Note: Using a doorbell (chimes are preferred for TestSound)")
	} else if len(devices.StickUpCams) > 0 {
		// Fall back to stickup cam
		deviceID = devices.StickUpCams[0].ID
		deviceName = devices.StickUpCams[0].Name
		deviceType = "StickUp Cam"
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: %s)\n", deviceName, deviceID, deviceType)
		fmt.Println("  Note: Using a stickup cam (chimes are preferred for TestSound)")
	} else {
		// Use first other device
		deviceID = devices.Other[0].ID
		deviceName = devices.Other[0].Name
		deviceType = "Other"
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: %s)\n", deviceName, deviceID, deviceType)
		fmt.Println("  Note: Using an other device (chimes are preferred for TestSound)")
	}
	fmt.Println()

	// Step 3: Test chime sound with "ding" kind
	fmt.Printf("Step 3: Testing chime sound (ding) on device %s...\n", deviceName)
	err = client.TestSound(ctx, ring.TestSoundRequest{
		DeviceID: deviceID,
		Kind:     "ding",
	})
	if err != nil {
		log.Fatalf("Failed to test sound: %v", err)
	}
	fmt.Printf("✓ Successfully triggered 'ding' sound on %s\n", deviceName)
	fmt.Println()

	// Optional: Test "motion" sound as well
	fmt.Printf("Step 4: Testing chime sound (motion) on device %s...\n", deviceName)
	err = client.TestSound(ctx, ring.TestSoundRequest{
		DeviceID: deviceID,
		Kind:     "motion",
	})
	if err != nil {
		log.Printf("Warning: Failed to test 'motion' sound: %v (this may not be supported on all devices)\n", err)
	} else {
		fmt.Printf("✓ Successfully triggered 'motion' sound on %s\n", deviceName)
	}
	fmt.Println()

	fmt.Println("Example completed successfully!")
}
