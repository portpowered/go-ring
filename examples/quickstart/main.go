package main

import (
	"context"
	"fmt"
	"os"

	"github.com/portpowered/go-ring/examples/internal/exampleerrors"
	"github.com/portpowered/go-ring/pkg/ring"
)

func main() {
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	client, err := ring.NewClient()
	if err != nil {
		return exampleerrors.Wrap("create Ring client", err)
	}

	auth := ring.AuthContext{AccessToken: os.Getenv("RING_ACCESS_TOKEN"), HardwareID: ""}

	devices, err := client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: auth})
	if err != nil {
		return exampleerrors.Wrap("list Ring devices", err)
	}

	fmt.Println(len(devices.Devices))

	return nil
}
