// Example chime-sound plays the ding test sound on an explicitly selected chime.
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
	token, chimeID := os.Getenv("RING_ACCESS_TOKEN"), os.Getenv("RING_CHIME_ID")
	if token == "" || chimeID == "" {
		return ringapimodels.NewBadRequestError("set RING_ACCESS_TOKEN and RING_CHIME_ID", nil)
	}

	client, err := ring.NewClient()
	if err != nil {
		return exampleerrors.Wrap("create Ring client", err)
	}

	auth := ring.AuthContext{AccessToken: token, HardwareID: ""}
	{
		err := client.TestSound(context.Background(), ring.TestSoundRequest{Auth: auth,
			DeviceID: chimeID,
			Sound:    ringapimodels.SoundKindDing,
		})
		if err != nil {
			return exampleerrors.Wrap("send chime test sound", err)
		}
	}

	fmt.Printf("Sent ding test sound to chime %s\n", chimeID)

	return nil
}
