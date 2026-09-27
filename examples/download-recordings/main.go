package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	// Get access token from environment variable
	accessToken := os.Getenv("RING_ACCESS_TOKEN")
	if accessToken == "" {
		return ringapimodels.NewBadRequestError("RING_ACCESS_TOKEN environment variable must be set", nil)
	}

	// Create client with access token
	client, err := ring.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	auth := ring.AuthContext{AccessToken: accessToken}

	// Step 1: List devices to select one
	fmt.Println("Step 1: Enumerating devices...")
	devices, err := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: auth})
	if err != nil {
		return ringapimodels.NewConnectionError("Failed to list devices", err)
	}

	if devices == nil {
		return ringapimodels.NewConnectionError("Devices response is nil", nil)
	}

	totalDevices := len(devices.Devices)
	if totalDevices == 0 {
		return ringapimodels.NewBadRequestError("No devices found. Cannot download recordings.", nil)
	}

	fmt.Printf("✓ Found %d total device(s)\n", totalDevices)
	fmt.Println()

	// Step 2: Select a device. Recording availability is checked by history.
	deviceID := devices.Devices[0].ID
	deviceName := devices.Devices[0].Name
	fmt.Printf("Step 2: Selected device: %s (ID: %s)\n", deviceName, deviceID)
	fmt.Println()

	// Step 3: Get device history (recordings)
	fmt.Printf("Step 3: Retrieving recording history for device %s...\n", deviceName)
	history, err := client.GetDeviceHistory(ctx, ring.GetDeviceHistoryRequest{Auth: auth,
		DeviceID: deviceID,
		Limit:    5,
		Kind:     "",
	}) // Get up to 5 recordings
	if err != nil {
		return ringapimodels.NewConnectionError("Failed to get device history", err)
	}

	if history == nil {
		return ringapimodels.NewConnectionError("History response is nil", nil)
	}

	if len(history.Recordings) == 0 {
		fmt.Printf("No recordings found for device %s\n", deviceName)
		fmt.Println("Example completed (no recordings to download)")
		return nil
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
		filename := fmt.Sprintf("recording_%d_%s.mp4", recording.ID, safeFilenamePart(recording.Kind))
		filepath := filepath.Join(".", filename)

		fmt.Printf("  Downloading recording %d/%d (ID: %d, Kind: %s)...\n",
			i+1, len(history.Recordings), recording.ID, recording.Kind)

		// Get the video stream
		stream, err := client.GetRecording(ctx, ring.GetRecordingRequest{Auth: auth,
			RecordingID: recording.ID,
		})
		if err != nil {
			log.Printf("  ✗ Failed to get recording %d: %v\n", recording.ID, err)
			continue
		}

		// Create the file
		out, err := os.Create(filepath) // #nosec G304 -- filename contains only sanitized characters and stays in the current directory.
		if err != nil {
			log.Printf("  ✗ Failed to create file %s: %v\n", filepath, err)
			_ = stream.Body.Close()
			continue
		}

		// Write the stream to file
		written, err := io.Copy(out, stream.Body)
		closeErr := out.Close()
		bodyCloseErr := stream.Body.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = bodyCloseErr
		}

		if err != nil {
			log.Printf("  ✗ Failed to write recording %d to file: %v\n", recording.ID, err)
			if removeErr := os.Remove(filepath); removeErr != nil {
				log.Printf("  ✗ Failed to remove incomplete recording %s: %v\n", filepath, removeErr)
			}
			continue
		}

		fmt.Printf("  ✓ Downloaded to %s (%d bytes, Content-Type: %s)\n", filepath, written, stream.ContentType)
	}
	fmt.Println()

	fmt.Println("Example completed successfully!")
	return nil
}

func safeFilenamePart(value string) string {
	part := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, value)
	part = strings.Trim(part, "_")
	if part == "" {
		return "unknown"
	}
	return part
}
