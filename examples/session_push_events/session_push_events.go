// Example session_push_events subscribes to device push notifications.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"github.com/portpowered/go-ring/pkg/ring"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	token := os.Getenv("RING_ACCESS_TOKEN")
	deviceID, err := strconv.ParseInt(os.Getenv("RING_DEVICE_ID"), 10, 64)
	if token == "" || err != nil || deviceID <= 0 {
		return errors.New("set RING_ACCESS_TOKEN and a positive RING_DEVICE_ID")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	client, err := ring.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()
	auth := ring.AuthContext{AccessToken: token}
	conn, err := client.OpenSignaling(ctx, ring.OpenSignalingRequest{Auth: auth})
	if err != nil {
		return err
	}
	defer conn.Close()
	subscription, err := conn.SubscribePush(ctx, []ring.PushFilter{{
		FilterIdentifier:  "device-events",
		Filters:           ring.PushFilters{DoorbotIDs: []int64{deviceID}},
		NotificationScope: "event",
		NotificationType:  "shoulder_tap",
	}})
	if err != nil {
		return err
	}
	defer subscription.Close()

	fmt.Println("Listening for device push events; Ctrl+C closes the subscription.")
	for {
		event, err := subscription.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		fmt.Printf("%s: %s\n", event.NotificationType, event.Payload)
	}
}
