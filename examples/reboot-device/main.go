// Example reboot-device sends the recorded reboot command to one device.
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
	token, deviceID := os.Getenv("RING_ACCESS_TOKEN"), os.Getenv("RING_DEVICE_ID")
	if token == "" || deviceID == "" {
		return ringapimodels.NewBadRequestError("set RING_ACCESS_TOKEN and RING_DEVICE_ID", nil)
	}

	client, err := ring.NewClient()
	if err != nil {
		return exampleerrors.Wrap("create Ring client", err)
	}

	auth := ring.AuthContext{AccessToken: token, HardwareID: ""}
	{
		err := client.RebootDevice(context.Background(), ring.DeviceIDRequest{Auth: auth, DeviceID: deviceID})
		if err != nil {
			return exampleerrors.Wrap("send device reboot request", err)
		}
	}

	fmt.Printf("Reboot request accepted for device %s\n", deviceID)

	return nil
}
