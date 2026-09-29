package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/go-ring/pkg/ring"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
	"golang.org/x/term"
)

type storedTokens struct {
	ringapimodels.AuthResponse

	HardwareID string    `json:"hardware_id"`
	ReceivedAt time.Time `json:"received_at"`
}

type tokenStore struct {
	path          string
	clientOptions []ring.Option
}

func (s tokenStore) load() (storedTokens, error) {
	var tokens storedTokens

	data, err := os.ReadFile(s.path)
	if err != nil {
		return tokens, wrapCommandError("read token file", err)
	}

	err = json.Unmarshal(data, &tokens)
	if err != nil {
		return tokens, wrapCommandError("invalid token file", err)
	}

	if tokens.AccessToken == "" || tokens.HardwareID == "" || tokens.ReceivedAt.IsZero() {
		return tokens, commandError("incomplete token file")
	}

	return tokens, nil
}

func (s tokenStore) save(tokens storedTokens) error {
	err := os.MkdirAll(filepath.Dir(s.path), privateDirMode)
	if err != nil {
		return wrapCommandError("create token directory", err)
	}

	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return wrapCommandError("encode token file", err)
	}

	file, err := os.CreateTemp(filepath.Dir(s.path), ".tokens-*")
	if err != nil {
		return wrapCommandError("create temporary token file", err)
	}

	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()

	err = file.Chmod(privateFileMode)
	if err != nil {
		return wrapCommandError("set token file permissions", err)
	}

	err = restrictTokenFile(file.Name())
	if err != nil {
		return wrapCommandError("set token file permissions", err)
	}

	_, err = file.Write(append(data, '\n'))
	if err != nil {
		return wrapCommandError("write token file", err)
	}

	err = file.Sync()
	if err != nil {
		return wrapCommandError("sync token file", err)
	}

	err = file.Close()
	if err != nil {
		return wrapCommandError("close token file", err)
	}

	err = os.Rename(file.Name(), s.path)
	if err != nil {
		return wrapCommandError("save token file", err)
	}

	return nil
}

func login(ctx context.Context, store tokenStore, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)

	username, err := prompt(reader, out, "Username: ")
	if err != nil {
		return err
	}

	password, err := readLoginPassword(reader, in, out)
	if err != nil {
		return err
	}

	if username == "" || password == "" {
		return commandError("username and password required")
	}

	identity := uuid.NewString()

	client, err := ring.NewClient(store.clientOptions...)
	if err != nil {
		return wrapCommandError("create Ring client", err)
	}

	defer func() { _ = client.Close() }()

	session, err := client.NewLoginSession(ring.LoginSessionRequest{
		Username:   username,
		Password:   password,
		HardwareID: identity,
	})
	if err != nil {
		return wrapCommandError("start login", err)
	}

	defer func() { _ = session.Close() }()

	otp := os.Getenv("RING_OTP_CODE")

	response, err := session.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otp})

	if ringapimodels.IsRequires2FAError(err) && otp == "" {
		otp, err = prompt(reader, out, "Verification code: ")
		if err != nil {
			return err
		}

		if otp == "" {
			return commandError("verification code required")
		}

		response, err = session.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otp})
	}

	if err != nil {
		return wrapCommandError("authenticate", err)
	}

	err = store.save(storedTokens{AuthResponse: *response, HardwareID: identity, ReceivedAt: time.Now()})
	if err != nil {
		return wrapCommandError("save login", err)
	}

	_, _ = fmt.Fprintf(out, "Authenticated; tokens saved to %s\n", store.path)

	return nil
}

func readLoginPassword(reader *bufio.Reader, in io.Reader, out io.Writer) (string, error) {
	password := os.Getenv("RING_PASSWORD")
	if password != "" {
		return password, nil
	}

	_, _ = fmt.Fprint(out, "Password: ")

	file, ok := in.(*os.File)
	if ok && term.IsTerminal(int(file.Fd())) {
		passwordBytes, err := term.ReadPassword(int(file.Fd()))
		_, _ = fmt.Fprintln(out)

		if err != nil {
			return "", wrapCommandError("read password", err)
		}

		return string(passwordBytes), nil
	}

	password, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", wrapCommandError("read password", err)
	}

	return strings.TrimSpace(password), nil
}

func prompt(reader *bufio.Reader, out io.Writer, label string) (string, error) {
	_, _ = fmt.Fprint(out, label)

	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", wrapCommandError("read command input", err)
	}

	return strings.TrimSpace(value), nil
}

func authStatus(store tokenStore, out io.Writer) error {
	tokens, err := store.load()
	if err != nil {
		return err
	}

	expiresAt := tokens.ReceivedAt.Add(time.Duration(tokens.ExpiresIn) * time.Second).Format(time.RFC3339)
	_, _ = fmt.Fprintf(out, "Saved login at %s; access token expires around %s\n", store.path, expiresAt)

	return nil
}
