package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

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

	// Step 1: List devices to select one
	fmt.Println("Step 1: Enumerating devices...")
	devices, err := client.ListDevices(ctx)
	if err != nil {
		log.Fatalf("Failed to list devices: %v", err)
	}

	if devices == nil {
		log.Fatal("Devices response is nil")
	}

	totalDevices := len(devices.Doorbells) + len(devices.Chimes) + len(devices.StickUpCams) + len(devices.Other)
	if totalDevices == 0 {
		log.Fatal("No devices found. Cannot download recordings.")
	}

	fmt.Printf("✓ Found %d total device(s)\n", totalDevices)
	fmt.Println()

	// Step 2: Select a device (prefer doorbell for recordings)
	var deviceID string
	var deviceName string

	if len(devices.Doorbells) > 0 {
		deviceID = devices.Doorbells[0].ID
		deviceName = devices.Doorbells[0].Name
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: Doorbell)\n", deviceName, deviceID)
	} else if len(devices.StickUpCams) > 0 {
		deviceID = devices.StickUpCams[0].ID
		deviceName = devices.StickUpCams[0].Name
		fmt.Printf("Step 2: Selected device: %s (ID: %s, Type: StickUp Cam)\n", deviceName, deviceID)
	} else {
		log.Fatal("No doorbells or stickup cams found. These device types typically have recordings.")
	}
	fmt.Println()

	// Step 3: Get device history (recordings)
	fmt.Printf("Step 3: Retrieving recording history for device %s...\n", deviceName)
	history, err := client.GetDeviceHistory(ctx, ring.GetDeviceHistoryRequest{
		DeviceID: deviceID,
		Limit:    5,
		Kind:     "",
	}) // Get up to 5 recordings
	if err != nil {
		log.Fatalf("Failed to get device history: %v", err)
	}

	if history == nil {
		log.Fatal("History response is nil")
	}

	if len(history.Recordings) == 0 {
		fmt.Printf("No recordings found for device %s\n", deviceName)
		fmt.Println("Example completed (no recordings to download)")
		return
	}

	fmt.Printf("✓ Found %d recording(s)\n", len(history.Recordings))
	for i, recording := range history.Recordings {
		fmt.Printf("  %d. ID: %d, Kind: %s, Created: %s\n", i+1, recording.ID, recording.Kind, recording.CreatedAt)
	}
	fmt.Println()

	// Step 4: Download recordings
	fmt.Println("Step 4: Downloading recordings...")
	for i, recording := range history.Recordings {
		// Create filename based on recording ID
		filename := fmt.Sprintf("recording_%d_%s.mp4", recording.ID, recording.Kind)
		filepath := filepath.Join(".", filename)

		fmt.Printf("  Downloading recording %d/%d (ID: %d, Kind: %s)...\n",
			i+1, len(history.Recordings), recording.ID, recording.Kind)

		// Get the video stream
		stream, err := client.GetRecording(ctx, ring.GetRecordingRequest{
			RecordingID: recording.ID,
		})
		if err != nil {
			log.Printf("  ✗ Failed to get recording %d: %v\n", recording.ID, err)
			continue
		}
		defer stream.Body.Close()

		// Create the file
		out, err := os.Create(filepath)
		if err != nil {
			log.Printf("  ✗ Failed to create file %s: %v\n", filepath, err)
			stream.Body.Close()
			continue
		}

		// Write the stream to file
		written, err := io.Copy(out, stream.Body)
		out.Close()
		stream.Body.Close()

		if err != nil {
			log.Printf("  ✗ Failed to write recording %d to file: %v\n", recording.ID, err)
			os.Remove(filepath) // Clean up partial file
			continue
		}

		fmt.Printf("  ✓ Downloaded to %s (%d bytes, Content-Type: %s)\n", filepath, written, stream.ContentType)
	}
	fmt.Println()

	fmt.Println("Example completed successfully!")
}
