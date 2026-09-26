// Example chime-sound plays the ding test sound on an explicitly selected chime.
package main

import (
	"context"
	"fmt"
	"os"

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
	token, chimeID := os.Getenv("RING_ACCESS_TOKEN"), os.Getenv("RING_CHIME_ID")
	if token == "" || chimeID == "" {
		return fmt.Errorf("set RING_ACCESS_TOKEN and RING_CHIME_ID")
	}
	client, err := ring.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()
	auth := ring.AuthContext{AccessToken: token}
	if err := client.TestSound(context.Background(), ring.TestSoundRequest{Auth: auth,
		DeviceID: chimeID,
		Sound:    ringapimodels.SoundKindDing,
	}); err != nil {
		return err
	}
	fmt.Printf("Sent ding test sound to chime %s\n", chimeID)
	return nil
}
