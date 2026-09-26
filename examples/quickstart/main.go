package main

import (
	"context"
	"fmt"
	"github.com/portpowered/go-ring/pkg/ring"
	"log"
	"os"
)

func main() {
	client, err := ring.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	auth := ring.AuthContext{AccessToken: os.Getenv("RING_ACCESS_TOKEN")}
	devices, err := client.ListDevices(context.Background(), ring.ListDevicesRequest{Auth: auth})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(devices.Doorbells))
}
