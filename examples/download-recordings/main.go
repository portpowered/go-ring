package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

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

	// Get access token from environment variable
	accessToken := os.Getenv("RING_ACCESS_TOKEN")
	if accessToken == "" {
		return ringapimodels.NewBadRequestError("RING_ACCESS_TOKEN environment variable must be set", nil)
	}

	// Create client with access token
	client, err := ring.NewClient()
	if err != nil {
		return exampleerrors.Wrap("create Ring client", err)
	}

	auth := ring.AuthContext{AccessToken: accessToken, HardwareID: ""}

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
		DeviceID:  deviceID,
		Limit:     5,
		Kind:      "",
		OlderThan: nil,
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

	downloadRecordings(ctx, client, auth, history.Recordings)

	return nil
}

func downloadRecordings(
	ctx context.Context,
	client *ring.Client,
	auth ring.AuthContext,
	recordings []ringapimodels.Recording,
) {
	fmt.Printf("✓ Found %d recording(s)\n", len(recordings))

	for i, recording := range recordings {
		fmt.Printf("  %d. ID: %d, Kind: %s, Created: %s\n", i+1, recording.ID, recording.Kind, recording.CreatedAt)
	}

	fmt.Println()
	fmt.Println("Step 4: Downloading recordings...")

	for recordingIndex, recording := range recordings {
		fmt.Printf("  Downloading recording %d/%d (ID: %d, Kind: %s)...\n",
			recordingIndex+1, len(recordings), recording.ID, recording.Kind)
		downloadRecording(ctx, client, auth, recording)
	}

	fmt.Println()
	fmt.Println("Example completed successfully!")
}
func downloadRecording(
	ctx context.Context,
	client *ring.Client,
	auth ring.AuthContext,
	recording ringapimodels.Recording,
) {
	filename := fmt.Sprintf("recording_%d_%s.mp4", recording.ID, safeFilenamePart(recording.Kind))
	path := filepath.Join(".", filename)

	stream, err := client.GetRecording(ctx, ring.GetRecordingRequest{Auth: auth, RecordingID: recording.ID})
	if err != nil {
		log.Printf("  ✗ Failed to get recording %d: %v\n", recording.ID, err)

		return
	}

	out, err := os.Create(
		path,
	) // #nosec G304 -- filename contains only sanitized characters and stays in the current directory.
	if err != nil {
		log.Printf("  ✗ Failed to create file %s: %v\n", path, err)
		closeIgnoringError(stream.Body.Close)

		return
	}

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

		removeErr := os.Remove(path)
		if removeErr != nil {
			log.Printf("  ✗ Failed to remove incomplete recording %s: %v\n", path, removeErr)
		}

		return
	}

	fmt.Printf("  ✓ Downloaded to %s (%d bytes, Content-Type: %s)\n", path, written, stream.ContentType)
}

func closeIgnoringError(closeFunc func() error) {
	_ = closeFunc()
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
