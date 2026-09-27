package main

import (
	"context"
	"fmt"
	"github.com/portpowered/go-ring/pkg/ring"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	client, err := ring.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	auth := ring.AuthContext{AccessToken: os.Getenv("RING_ACCESS_TOKEN")}
	devices, err := client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: auth})
	if err != nil {
		return err
	}
	fmt.Println(len(devices.Doorbells))
	return nil
}
