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
	err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout)
	if err != nil {
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
		return wrapCommandError("find user configuration directory", err)
	}

	defaultTokens := filepath.Join(config, "go-ring", "tokens.json")
	cmd := flag.NewFlagSet("go-ring", flag.ContinueOnError)
	cmd.SetOutput(io.Discard)
	tokenFile := cmd.String("token-file", defaultTokens, "token file")
	apiBase := cmd.String("api-base", "", "API origin override for local testing")
	oauthBase := cmd.String("oauth-base", "", "OAuth origin override for local testing")
	solutionsBase := cmd.String("solutions-base", "", "Solutions origin override for local testing")

	signalingURL := cmd.String("signaling-url", "", "signaling socket override for local testing")

	err = cmd.Parse(args)
	if err != nil {
		return wrapCommandError("parse command flags", err)
	}

	args = cmd.Args()
	if len(args) == 0 {
		return usage(out)
	}

	endpoints := ring.Endpoints{
		APIBaseURL:       *apiBase,
		OAuthBaseURL:     *oauthBase,
		SolutionsBaseURL: *solutionsBase,
		SignalingURL:     *signalingURL,
	}

	for _, raw := range []string{*apiBase, *oauthBase, *solutionsBase, *signalingURL} {
		err := safeEndpoint(raw)
		if err != nil {
			return err
		}
	}

	store := tokenStore{path: *tokenFile, clientOptions: nil}
	if *apiBase != "" || *oauthBase != "" || *solutionsBase != "" || *signalingURL != "" {
		store.clientOptions = []ring.Option{ring.WithEndpoints(endpoints)}
	}

	switch args[0] {
	case "auth":
		return authCommand(ctx, store, args[1:], in, out)
	case "devices":
		if len(args) == 2 && args[1] == "list" {
			return withClient(ctx, store, func(client *ring.Client, auth ring.AuthContext) error {
				return listDevices(ctx, client, auth, out)
			})
		}
	case "snapshot":
		return snapshotCommand(ctx, store, args[1:], out)
	case "siren":
		return sirenCommand(ctx, store, args[1:], out)
	case "reboot":
		return rebootCommand(ctx, store, args[1:], out)
	case "health":
		return healthCommand(ctx, store, args[1:], out)
	case "sound":
		return soundCommand(ctx, store, args[1:], out)
	case "view":
		return viewCommand(ctx, store, args[1:], in, out)
	case "events":
		return eventsCommand(ctx, store, args[1:], out)
	case "replay-video":
		return replayVideoCommand(args[1:], out)
	}

	return usage(out)
}

func authCommand(ctx context.Context, store tokenStore, args []string, in io.Reader, out io.Writer) error {
	if len(args) != 1 {
		return usage(out)
	}

	switch args[0] {
	case "login":
		return login(ctx, store, in, out)
	case "status":
		return authStatus(store, out)
	case "logout":
		err := os.Remove(store.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return wrapCommandError("remove saved login", err)
		}

		pushPath := filepath.Join(filepath.Dir(store.path), "push.json")

		err = os.Remove(pushPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return wrapCommandError("remove saved FCM credentials", err)
		}

		_, _ = fmt.Fprintln(out, "Local tokens and push credentials removed")

		return nil
	default:
		return usage(out)
	}
}

func usage(out io.Writer) error {
	const usageText = "Usage: go-ring [--token-file path] auth login|status|logout | devices list | " +
		"snapshot <id> --output file [--timeout 30s] [--ice-servers file.json] | siren <id> on|off | reboot <id> | " +
		"health <id> [--refresh] | sound <chime-id> ding|motion | " +
		"view <id> [--player ffplay] [--debug] [--record-rtp file] " +
		"[--ice-servers file.json] [--continuous] [--speed 0.5] | events watch <id> [--duration 60s] | " +
		"replay-video <recording> --output file.h264"

	_, _ = fmt.Fprintln(out, usageText)

	return commandError("invalid command")
}

func safeEndpoint(raw string) error {
	if raw == "" {
		return nil
	}

	parsedURL, err := url.Parse(raw)
	if err != nil {
		return wrapCommandError("parse endpoint override", err)
	}

	if parsedURL.Scheme == "https" || parsedURL.Scheme == "wss" {
		return nil
	}

	if (parsedURL.Scheme == loopbackHTTPScheme || parsedURL.Scheme == "ws") && isLoopbackHost(parsedURL.Hostname()) {
		return nil
	}

	return commandError("insecure endpoint overrides must use loopback")
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}

	address := net.ParseIP(host)

	return address != nil && address.IsLoopback()
}

func withClient(ctx context.Context, store tokenStore, action func(*ring.Client, ring.AuthContext) error) error {
	tokens, err := store.load()
	if err != nil {
		return wrapCommandError("load saved login", err)
	}

	client, err := ring.NewClient(store.clientOptions...)
	if err != nil {
		return wrapCommandError("create Ring client", err)
	}

	defer func() { _ = client.Close() }()

	if time.Now().After(tokens.ReceivedAt.Add(time.Duration(tokens.ExpiresIn-refreshSkewSeconds) * time.Second)) {
		if tokens.RefreshToken == "" {
			return commandError("token expired; run auth login")
		}

		refreshed, refreshErr := client.RefreshToken(ctx, ring.RefreshTokenRequest{
			RefreshToken: tokens.RefreshToken,
			HardwareID:   tokens.HardwareID,
		})
		if refreshErr != nil {
			return wrapCommandError("refresh login", refreshErr)
		}

		if refreshed.RefreshToken == "" {
			refreshed.RefreshToken = tokens.RefreshToken
		}

		tokens = storedTokens{AuthResponse: *refreshed, HardwareID: tokens.HardwareID, ReceivedAt: time.Now()}

		err := store.save(tokens)
		if err != nil {
			return wrapCommandError("save refreshed login", err)
		}
	}

	return action(client, ring.AuthContext{AccessToken: tokens.AccessToken, HardwareID: tokens.HardwareID})
}

func snapshotCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) < 1 || strings.HasPrefix(args[0], "-") {
		return commandError("snapshot requires a device ID")
	}

	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "", "image file")
	iceFile := flags.String("ice-servers", "", "JSON array of ICE servers")

	timeout := flags.Duration("timeout", snapshotFrameTimeout, "maximum time to capture a live frame")

	err := flags.Parse(args[1:])
	if err != nil {
		return wrapCommandError("parse snapshot flags", err)
	}

	if *output == "" || flags.NArg() != 0 || *timeout <= 0 || *timeout > 2*time.Minute {
		return commandError("snapshot requires --output file and a timeout between 0 and 2 minutes")
	}

	return withClient(ctx, store, func(client *ring.Client, auth ring.AuthContext) error {
		picture, err := captureRTCSnapshot(ctx, client, auth, args[0], *iceFile, *timeout)
		if err != nil {
			return err
		}

		err = os.WriteFile(*output, picture, privateFileMode)
		if err != nil {
			return wrapCommandError("save snapshot", err)
		}

		_, _ = fmt.Fprintf(out, "Saved %s (image/jpeg from live view)\n", *output)

		return nil
	})
}

func sirenCommand(ctx context.Context, store tokenStore, args []string, out io.Writer) error {
	if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
		return commandError("usage: siren <device-id> on|off")
	}

	return withClient(ctx, store, func(client *ring.Client, auth ring.AuthContext) error {
		err := client.SetSiren(ctx, ring.SetSirenRequest{Auth: auth, DeviceID: args[0], Enabled: args[1] == "on"})
		if err != nil {
			return wrapCommandError("set siren", err)
		}

		_, _ = fmt.Fprintf(out, "Siren %s request acknowledged\n", args[1])

		return nil
	})
}

func listDevices(ctx context.Context, client *ring.Client, auth ring.AuthContext, out io.Writer) error {
	devices, err := client.ListDevices(ctx, ring.ListDevicesRequest{Auth: auth})
	if err != nil {
		return wrapCommandError("list devices", err)
	}

	for _, device := range devices.Devices {
		_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", device.ID, device.Name, device.Type, device.Kind)
	}

	return nil
}
