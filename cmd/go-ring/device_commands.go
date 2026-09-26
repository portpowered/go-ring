package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func rebootCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("usage: reboot <device-id>")
	}
	return withClient(ctx, store, func(client *ring.Client, auth ring.AuthContext) error {
		if err := client.RebootDevice(ctx, ring.DeviceIDRequest{Auth: auth, DeviceID: args[0]}); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "Reboot request acknowledged; the device may be unavailable while restarting")
		return nil
	})
}

func healthCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "" {
		return errors.New("usage: health <device-id> [--refresh]")
	}
	flags := flag.NewFlagSet("health", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	refresh := flags.Bool("refresh", false, "also query the legacy health endpoint")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: health <device-id> [--refresh]")
	}
	return withClient(ctx, store, func(client *ring.Client, auth ring.AuthContext) error {
		detail, err := client.GetDeviceDetail(ctx, ring.GetDeviceDetailRequest{Auth: auth, DeviceID: args[0]})
		if err != nil {
			return err
		}
		device := detail.Device
		_, _ = fmt.Fprintf(out, "Device %d (%s)\n", device.ID, device.Description)
		printHealthValue(out, "Wi-Fi name", device.WifiName)
		printHealthValue(out, "Wi-Fi signal strength", device.WifiSignalStrength)
		if device.Health != nil {
			printHealthValue(out, "Battery level", device.Health.BatteryLevel)
			printHealthValue(out, "Battery status", device.Health.BatteryStatus)
			printHealthValue(out, "Connected", device.Health.Connected)
			printHealthValue(out, "Signal strength", device.Health.SignalStrength)
			printHealthValue(out, "RSSI", device.Health.RSSI)
			printHealthValue(out, "Firmware", device.Health.FirmwareVersion)
			printHealthValue(out, "PTZ connected", device.Health.PTZConnected)
			printHealthValue(out, "Last update", device.Health.LastUpdate)
		} else {
			_, _ = fmt.Fprintln(out, "Health: unavailable in device detail")
		}
		if !*refresh {
			return nil
		}
		health, err := client.UpdateDeviceHealth(ctx, ring.UpdateDeviceHealthRequest{Auth: auth, DeviceID: args[0]})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, "Legacy health endpoint:")
		printHealthValue(out, "Battery level", health.BatteryLevel)
		printHealthValue(out, "Battery status", health.BatteryStatus)
		printHealthValue(out, "Signal strength", health.SignalStrength)
		printHealthValue(out, "Firmware", health.FirmwareVersion)
		printHealthValue(out, "Last update", health.LastUpdate)
		return nil
	})
}

func printHealthValue[T any](out io.Writer, label string, value *T) {
	if value == nil {
		_, _ = fmt.Fprintf(out, "%s: unavailable\n", label)
		return
	}
	_, _ = fmt.Fprintf(out, "%s: %v\n", label, *value)
}

func soundCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) != 2 || args[0] == "" {
		return errors.New("usage: sound <chime-id> ding|motion")
	}
	sound := ringapimodels.SoundKind(args[1])
	if !sound.Valid() {
		return errors.New("sound must be ding or motion")
	}
	return withClient(ctx, store, func(client *ring.Client, auth ring.AuthContext) error {
		if err := client.TestSound(ctx, ring.TestSoundRequest{Auth: auth, DeviceID: args[0], Sound: sound}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Chime %s test request acknowledged\n", sound)
		return nil
	})
}
