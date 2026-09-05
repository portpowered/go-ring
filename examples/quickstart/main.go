package main

import (
	"context"
	"fmt"
	"github.com/portpowered/go-ring/pkg/ring"
	"log"
	"os"
)

func main() {
	client, err := ring.NewClient(ring.WithAccessToken(os.Getenv("RING_ACCESS_TOKEN")))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
	devices, err := client.ListDevices(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(devices.Doorbells))
}
