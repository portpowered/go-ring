package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/portpowered/go-ring/pkg/ring"
)

func eventsCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) < 2 || args[0] != "watch" {
		return usage(out)
	}

	deviceID := args[1]
	flags := flag.NewFlagSet("events watch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	duration := flags.Duration("duration", time.Minute, "time to watch for events")
	ding := flags.Bool("ding", false, "subscribe to ding events")

	motion := flags.Bool("motion", true, "subscribe to motion events")

	err := flags.Parse(args[2:])
	if err != nil {
		return wrapCommandError("parse event watch flags", err)
	}

	if len(flags.Args()) != 0 || *duration <= 0 {
		return usage(out)
	}

	watchCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	watchCtx, cancel := context.WithTimeout(watchCtx, *duration)
	defer cancel()

	credentialsPath := filepath.Join(filepath.Dir(store.path), "push.json")

	credentials, err := os.ReadFile(credentialsPath) // #nosec G304 -- User-selected config path.
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return wrapCommandError("read FCM credentials", err)
	}

	return withClient(watchCtx, store, func(client *ring.Client, auth ring.AuthContext) error {
		connection, connectErr := client.ConnectPush(watchCtx, ring.ConnectPushRequest{
			Auth:        auth,
			Credentials: credentials,
			DeviceIDs:   []string{deviceID},
			Ding:        *ding,
			Motion:      *motion,
		})
		if connectErr != nil {
			return wrapCommandError("connect to FCM", connectErr)
		}

		defer func() { _ = connection.Close() }()

		return watchPushEvents(watchCtx, connection, credentialsPath, out)
	})
}

func watchPushEvents(
	ctx context.Context,
	connection *ring.PushConnection,
	credentialsPath string,
	out io.Writer,
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-connection.Events():
			if !ok {
				return nil
			}

			err := writePushEvent(credentialsPath, event, out)
			if err != nil {
				return err
			}
		}
	}
}

func writePushEvent(credentialsPath string, event ring.FCMEvent, out io.Writer) error {
	if event.Err != nil {
		_, _ = fmt.Fprintf(out, "FCM %s: %v\n", event.Kind, event.Err)
	} else {
		switch event.Kind {
		case ring.PushCredentials:
			err := savePushCredentials(credentialsPath, event.Credentials)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintln(out, "FCM credentials saved")
		case ring.PushMessage:
			_, _ = fmt.Fprintf(out, "FCM message: device=%s action=%s\n", event.DeviceID, event.Action)
		case ring.PushRegistered, ring.PushConnected, ring.PushRetry, ring.PushClosed:
			_, _ = fmt.Fprintf(out, "FCM %s\n", event.Kind)
		default:
			_, _ = fmt.Fprintf(out, "FCM %s\n", event.Kind)
		}
	}

	return nil
}

func savePushCredentials(path string, credentials json.RawMessage) error {
	if !json.Valid(credentials) {
		return commandError("FCM credentials are invalid JSON")
	}

	err := os.MkdirAll(filepath.Dir(path), privateDirMode)
	if err != nil {
		return wrapCommandError("create FCM credential directory", err)
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".push-*")
	if err != nil {
		return wrapCommandError("create temporary FCM credentials file", err)
	}

	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()

	err = file.Chmod(privateFileMode)
	if err != nil {
		return wrapCommandError("set FCM credential file permissions", err)
	}

	err = restrictTokenFile(file.Name())
	if err != nil {
		return wrapCommandError("set FCM credential file permissions", err)
	}

	_, err = file.Write(credentials)
	if err != nil {
		return wrapCommandError("write FCM credentials", err)
	}

	err = file.Sync()
	if err != nil {
		return wrapCommandError("sync FCM credentials", err)
	}

	err = file.Close()
	if err != nil {
		return wrapCommandError("close FCM credentials", err)
	}

	err = os.Rename(file.Name(), path)
	if err != nil {
		return wrapCommandError("save FCM credentials", err)
	}

	return nil
}
