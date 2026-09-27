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
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || *duration <= 0 {
		return usage(out)
	}
	watchCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	watchCtx, cancel := context.WithTimeout(watchCtx, *duration)
	defer cancel()
	credentialsPath := filepath.Join(filepath.Dir(store.path), "push.json")
	credentials, err := os.ReadFile(credentialsPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return withClient(watchCtx, store, func(client *ring.Client, auth ring.AuthContext) error {
		connection, connectErr := client.ConnectPush(watchCtx, ring.ConnectPushRequest{
			Auth: auth, Credentials: credentials, DeviceIDs: []string{deviceID}, Ding: *ding, Motion: *motion,
		})
		if connectErr != nil {
			return connectErr
		}
		defer func() { _ = connection.Close() }()
		for {
			select {
			case <-watchCtx.Done():
				return nil
			case event, ok := <-connection.Events():
				if !ok {
					return nil
				}
				if event.Kind == ring.PushCredentials {
					if err := savePushCredentials(credentialsPath, event.Credentials); err != nil {
						return err
					}
					_, _ = fmt.Fprintln(out, "FCM credentials saved")
					continue
				}
				if event.Err != nil {
					_, _ = fmt.Fprintf(out, "FCM %s: %v\n", event.Kind, event.Err)
					continue
				}
				if event.Kind == ring.PushMessage {
					_, _ = fmt.Fprintf(out, "FCM message: device=%s action=%s\n", event.DeviceID, event.Action)
					continue
				}
				_, _ = fmt.Fprintf(out, "FCM %s\n", event.Kind)
			}
		}
	})
}

func savePushCredentials(path string, credentials json.RawMessage) error {
	if !json.Valid(credentials) {
		return errors.New("FCM credentials are invalid JSON")
	}
	if err := os.MkdirAll(filepath.Dir(path), privateDirMode); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".push-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	if err := file.Chmod(privateFileMode); err != nil {
		return err
	}
	if err := restrictTokenFile(file.Name()); err != nil {
		return err
	}
	if _, err := file.Write(credentials); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
