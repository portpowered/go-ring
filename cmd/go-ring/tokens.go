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
		return tokens, err
	}
	if err := json.Unmarshal(data, &tokens); err != nil {
		return tokens, errors.New("invalid token file")
	}
	if tokens.AccessToken == "" || tokens.HardwareID == "" || tokens.ReceivedAt.IsZero() {
		return tokens, errors.New("incomplete token file")
	}
	return tokens, nil
}

func (s tokenStore) save(tokens storedTokens) error {
	if err := os.MkdirAll(filepath.Dir(s.path), privateDirMode); err != nil {
		return err
	}
	data, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".tokens-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(privateFileMode); err != nil {
		return err
	}
	if err := restrictTokenFile(file.Name()); err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}

func login(ctx context.Context, store tokenStore, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	username, err := prompt(reader, out, "Username: ")
	if err != nil {
		return err
	}
	password := os.Getenv("RING_PASSWORD")
	if password == "" {
		_, _ = fmt.Fprint(out, "Password: ")
		if file, ok := in.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
			bytes, readErr := term.ReadPassword(int(file.Fd()))
			_, _ = fmt.Fprintln(out)
			if readErr != nil {
				return readErr
			}
			password = string(bytes)
		} else {
			password, err = reader.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			password = strings.TrimSpace(password)
		}
	}
	if username == "" || password == "" {
		return errors.New("username and password required")
	}
	identity := uuid.NewString()
	client, err := ring.NewClient(store.clientOptions...)
	if err != nil {
		return err
	}
	defer client.Close()
	session, err := client.NewLoginSession(ring.LoginSessionRequest{Username: username, Password: password, HardwareID: identity})
	if err != nil {
		return err
	}
	defer session.Close()
	otp := os.Getenv("RING_OTP_CODE")
	response, err := session.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otp})
	if ringapimodels.IsRequires2FAError(err) && otp == "" {
		otp, err = prompt(reader, out, "Verification code: ")
		if err != nil {
			return err
		}
		if otp == "" {
			return errors.New("verification code required")
		}
		response, err = session.Authenticate(ctx, ring.CompleteLoginRequest{OTPCode: otp})
	}
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if err := store.save(storedTokens{AuthResponse: *response, HardwareID: identity, ReceivedAt: time.Now()}); err != nil {
		return fmt.Errorf("save login: %w", err)
	}
	_, _ = fmt.Fprintf(out, "Authenticated; tokens saved to %s\n", store.path)
	return nil
}

func prompt(reader *bufio.Reader, out io.Writer, label string) (string, error) {
	_, _ = fmt.Fprint(out, label)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func authStatus(store tokenStore, out io.Writer) error {
	tokens, err := store.load()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Saved login at %s; access token expires around %s\n", store.path, tokens.ReceivedAt.Add(time.Duration(tokens.ExpiresIn)*time.Second).Format(time.RFC3339))
	return nil
}
