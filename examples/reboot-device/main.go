// Example reboot-device sends the recorded reboot command to one device.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/portpowered/go-ring/pkg/ring"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	token, deviceID := os.Getenv("RING_ACCESS_TOKEN"), os.Getenv("RING_DEVICE_ID")
	if token == "" || deviceID == "" {
		return fmt.Errorf("set RING_ACCESS_TOKEN and RING_DEVICE_ID")
	}
	client, err := ring.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()
	auth := ring.AuthContext{AccessToken: token}
	if err := client.RebootDevice(context.Background(), ring.DeviceIDRequest{Auth: auth, DeviceID: deviceID}); err != nil {
		return err
	}
	fmt.Printf("Reboot request accepted for device %s\n", deviceID)
	return nil
}
