package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/portpowered/go-ring/pkg/ring"
)

const loopbackHTTPScheme = "http"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return usage(out)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("find user configuration directory: %w", err)
	}
	defaultTokens := filepath.Join(config, "go-ring", "tokens.json")
	cmd := flag.NewFlagSet("go-ring", flag.ContinueOnError)
	cmd.SetOutput(io.Discard)
	tokenFile := cmd.String("token-file", defaultTokens, "token file")
	apiBase := cmd.String("api-base", "", "API origin override for local testing")
	oauthBase := cmd.String("oauth-base", "", "OAuth origin override for local testing")
	solutionsBase := cmd.String("solutions-base", "", "Solutions origin override for local testing")
	signalingURL := cmd.String("signaling-url", "", "signaling socket override for local testing")
	if err := cmd.Parse(args); err != nil {
		return err
	}
	args = cmd.Args()
	if len(args) == 0 {
		return usage(out)
	}
	endpoints := ring.Endpoints{APIBaseURL: *apiBase, OAuthBaseURL: *oauthBase, SolutionsBaseURL: *solutionsBase, SignalingURL: *signalingURL}
	for _, raw := range []string{*apiBase, *oauthBase, *solutionsBase, *signalingURL} {
		if err := safeEndpoint(raw); err != nil {
			return err
		}
	}
	store := tokenStore{path: *tokenFile}
	if endpoints != (ring.Endpoints{}) {
		store.clientOptions = []ring.Option{ring.WithEndpoints(endpoints)}
	}
	switch args[0] {
	case "auth":
		if len(args) != 2 {
			return usage(out)
		}
		switch args[1] {
		case "login":
			return login(ctx, store, in, out)
		case "status":
			return authStatus(store, out)
		case "logout":
			if err := os.Remove(store.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			_, _ = fmt.Fprintln(out, "Local tokens removed")
			return nil
		}
	case "devices":
		if len(args) == 2 && args[1] == "list" {
			return withClient(ctx, store, func(client *ring.Client) error { return listDevices(ctx, client, out) })
		}
	case "snapshot":
		return snapshotCommand(ctx, store, args[1:], out)
	case "siren":
		return sirenCommand(ctx, store, args[1:], out)
	case "view":
		return viewCommand(ctx, store, args[1:], in, out)
	}
	return usage(out)
}

func usage(out io.Writer) error {
	_, _ = fmt.Fprintln(out, "Usage: go-ring [--token-file path] auth login|status|logout | devices list | snapshot <id> --output file | siren <id> on|off | view <id> [--player ffplay] [--ice-servers file.json] [--continuous] [--speed 0.5]")
	return errors.New("invalid command")
}

func safeEndpoint(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "https" || u.Scheme == "wss" {
		return nil
	}
	if (u.Scheme == loopbackHTTPScheme || u.Scheme == "ws") && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback() {
		return nil
	}
	if (u.Scheme == loopbackHTTPScheme || u.Scheme == "ws") && u.Hostname() == "localhost" {
		return nil
	}
	return errors.New("insecure endpoint overrides must use loopback")
}

func withClient(ctx context.Context, store tokenStore, action func(*ring.Client) error) error {
	tokens, err := store.load()
	if err != nil {
		return fmt.Errorf("load saved login: %w", err)
	}
	client, err := ring.NewClientWithToken(tokens.AccessToken, append([]ring.Option{ring.WithHardwareID(tokens.HardwareID)}, store.clientOptions...)...)
	if err != nil {
		return err
	}
	defer client.Close()
	if time.Now().After(tokens.ReceivedAt.Add(time.Duration(tokens.ExpiresIn-refreshSkewSeconds) * time.Second)) {
		if tokens.RefreshToken == "" {
			return errors.New("token expired; run auth login")
		}
		refreshed, refreshErr := client.RefreshToken(ctx, ring.RefreshTokenRequest{RefreshToken: tokens.RefreshToken, HardwareID: tokens.HardwareID})
		if refreshErr != nil {
			return fmt.Errorf("refresh login: %w", refreshErr)
		}
		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = tokens.RefreshToken
		}
		if err := client.Apply(ring.WithAccessToken(refreshed.AccessToken)); err != nil {
			return err
		}
		tokens = storedTokens{AuthResponse: *refreshed, HardwareID: tokens.HardwareID, ReceivedAt: time.Now()}
		if err := store.save(tokens); err != nil {
			return fmt.Errorf("save refreshed login: %w", err)
		}
	}
	return action(client)
}

func snapshotCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return errors.New("snapshot requires a device ID")
	}
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "", "image file")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *output == "" || flags.NArg() != 0 {
		return errors.New("snapshot requires --output file")
	}
	return withClient(ctx, store, func(client *ring.Client) error {
		picture, err := client.GetSnapshot(ctx, ring.GetSnapshotRequest{DeviceID: args[0], PollInterval: time.Second})
		if err != nil {
			return err
		}
		if err := os.WriteFile(*output, picture.Bytes, privateFileMode); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Saved %s (%s, %s)\n", *output, picture.ContentType, picture.Timestamp.Format(time.RFC3339))
		return nil
	})
}

func sirenCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
		return errors.New("usage: siren <device-id> on|off")
	}
	return withClient(ctx, store, func(client *ring.Client) error {
		if err := client.SetSiren(ctx, ring.SetSirenRequest{DeviceID: args[0], Enabled: args[1] == "on"}); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "Siren %s request acknowledged\n", args[1])
		return nil
	})
}

func listDevices(ctx context.Context, client *ring.Client, out io.Writer) error {
	devices, err := client.ListDevices(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices.GetAllDevices() {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\n", device.GetID(), device.GetName(), device.GetFamily())
	}
	return nil
}
