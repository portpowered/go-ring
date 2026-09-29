package push_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/pkg/dependencies/push"
	checkinPB "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/checkin"
	mcsPB "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type checkinRoundTripper func(*http.Request) (*http.Response, error)

func (transport checkinRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestFCMDialContextRunsMCSFramesOverOfflineTLS(t *testing.T) {
	t.Parallel()

	transcript := loadFCMLoginTranscript(t)
	require.Equal(t, "synthetic", transcript.Label)
	require.Equal(t, "MCS", transcript.Protocol)
	require.Equal(t, "login", transcript.Behavior)
	certificate, roots := syntheticTLSCertificate(t)
	serverConfig := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13}
	clientConfig := &tls.Config{RootCAs: roots, ServerName: protocol.MCSHost, MinVersion: tls.VersionTLS13}

	serverResult := make(chan error, 1)
	dialCalls := 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dial := push.DialContextFunc(func(_ context.Context, network, address string) (net.Conn, error) {
		dialCalls++

		if network != protocol.MCSNetwork || address != net.JoinHostPort(protocol.MCSHost, protocol.MCSPort) {
			return nil, io.ErrUnexpectedEOF
		}

		clientPipe, serverPipe := net.Pipe()

		go func() {
			defer func() { _ = serverPipe.Close() }()

			server := tls.Server(serverPipe, serverConfig)

			defer func() { _ = server.Close() }()

			handshakeErr := server.HandshakeContext(ctx)
			if handshakeErr != nil {
				serverResult <- handshakeErr

				return
			}

			version, tag, payload, err := readMCSFrame(server)
			if err != nil {
				serverResult <- err

				return
			}

			if version != transcript.Client.Version || tag != transcript.Client.Tag {
				serverResult <- io.ErrUnexpectedEOF

				return
			}

			err = matchMCSLoginPayload(payload, transcript.Client.Payload)
			if err != nil {
				serverResult <- err

				return
			}

			response, marshalErr := proto.Marshal(&mcsPB.LoginResponse{
				Id:                   proto.String(transcript.Server.ID),
				LastStreamIdReceived: proto.Int32(transcript.Server.LastStreamIDReceived),
				ServerTimestamp:      proto.Int64(transcript.Server.ServerTimestamp),
			})
			if marshalErr != nil {
				serverResult <- marshalErr

				return
			}

			frame := []byte{byte(transcript.Server.Tag)}
			frame = protowire.AppendVarint(frame, uint64(len(response)))

			frame = append(frame, response...)

			_, err = server.Write(append([]byte{byte(transcript.Server.Version)}, frame...))
			if err != nil {
				serverResult <- err

				return
			}

			serverResult <- exchangeMCSHeartbeat(server, transcript)
		}()

		return tls.Client(clientPipe, clientConfig), nil
	})

	responseBody, err := proto.Marshal(&checkinPB.AndroidCheckinResponse{StatsOk: proto.Bool(true)})
	require.NoError(t, err)

	transport := checkinRoundTripper(func(request *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "https://"+protocol.FCMCheckinHost+protocol.FCMCheckinPath, request.URL.String())

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(responseBody)),
		}, nil
	})
	credentials := json.RawMessage(`{"androidId":12345,"securityToken":67890,"token":"saved-token"}`)
	events, err := push.StartWithTransports(ctx, credentials, transport, dial)
	require.NoError(t, err)

	connected := false

	for event := range events {
		if event.Kind == push.KindConnected {
			connected = true

			break
		}

		if event.Kind == push.KindRetry {
			t.Fatalf("FCM receiver failed before the synthetic MCS login: %v", event.Err)
		}
	}

	require.True(t, connected, "MCS login response should produce a connected event")
	require.Equal(t, 1, dialCalls)

	select {
	case serverErr := <-serverResult:
		require.NoError(t, serverErr)
	case <-time.After(3 * time.Second):
		t.Fatal("offline MCS TLS peer did not finish the login exchange")
	}

	cancel()

	for range events {
	}
}

func exchangeMCSHeartbeat(peer io.ReadWriter, transcript mcsLoginTranscript) error {
	ping := new(mcsPB.HeartbeatPing)

	err := protojson.Unmarshal(transcript.ServerPing.Payload, ping)
	if err != nil {
		return ringerrors.NewBadRequestError("decode synthetic MCS heartbeat ping", err)
	}

	payload, err := proto.Marshal(ping)
	if err != nil {
		return ringerrors.NewBadRequestError("encode synthetic MCS heartbeat ping", err)
	}

	frame := []byte{byte(transcript.ServerPing.Tag)}
	frame = protowire.AppendVarint(frame, uint64(len(payload)))
	frame = append(frame, payload...)

	_, err = peer.Write(frame)
	if err != nil {
		return ringerrors.NewConnectionError("send synthetic MCS heartbeat ping", err)
	}

	tag, reply, err := readMCSTagFrame(peer)
	if err != nil {
		return ringerrors.NewConnectionError("read MCS heartbeat ack", err)
	}

	ack := new(mcsPB.HeartbeatAck)

	err = proto.Unmarshal(reply, ack)
	if err != nil {
		return ringerrors.NewBadRequestError("decode MCS heartbeat ack", err)
	}

	expectedAck := new(mcsPB.HeartbeatAck)

	err = protojson.Unmarshal(transcript.ClientAck.Payload, expectedAck)
	if err != nil {
		return ringerrors.NewBadRequestError("decode synthetic MCS heartbeat ack", err)
	}

	if tag != transcript.ClientAck.Tag || !proto.Equal(ack, expectedAck) {
		return io.ErrUnexpectedEOF
	}

	return nil
}

func readMCSTagFrame(reader io.Reader) (int32, []byte, error) {
	var tag [1]byte

	_, err := io.ReadFull(reader, tag[:])
	if err != nil {
		return 0, nil, err
	}

	length, err := readMCSVarint(reader)
	if err != nil {
		return int32(tag[0]), nil, err
	}

	payload := make([]byte, length)

	_, err = io.ReadFull(reader, payload)

	return int32(tag[0]), payload, err
}

func matchMCSLoginPayload(payload []byte, expectedJSON json.RawMessage) error {
	login := new(mcsPB.LoginRequest)

	err := proto.Unmarshal(payload, login)
	if err != nil {
		return ringerrors.NewBadRequestError("decode MCS login request", err)
	}

	expected := new(mcsPB.LoginRequest)

	err = protojson.Unmarshal(expectedJSON, expected)
	if err != nil {
		return ringerrors.NewBadRequestError("decode synthetic MCS login expectation", err)
	}

	// Compare field presence and unknown wire fields as well as known values.
	if !proto.Equal(login, expected) {
		return io.ErrUnexpectedEOF
	}

	return nil
}

func TestMCSLoginPairRejectsIncompleteOrUnknownOutboundPayload(t *testing.T) {
	t.Parallel()

	transcript := loadFCMLoginTranscript(t)
	expected := new(mcsPB.LoginRequest)

	err := protojson.Unmarshal(transcript.Client.Payload, expected)
	require.NoError(t, err)

	payload, err := proto.Marshal(expected)
	require.NoError(t, err)
	require.NoError(t, matchMCSLoginPayload(payload, transcript.Client.Payload))

	expected.Setting = nil

	missingSetting, err := proto.Marshal(expected)
	require.NoError(t, err)
	require.ErrorIs(t, matchMCSLoginPayload(missingSetting, transcript.Client.Payload), io.ErrUnexpectedEOF)

	withUnknown := protowire.AppendTag(payload, 99, protowire.VarintType)
	withUnknown = protowire.AppendVarint(withUnknown, 1)
	require.ErrorIs(t, matchMCSLoginPayload(withUnknown, transcript.Client.Payload), io.ErrUnexpectedEOF)
}

type mcsLoginTranscript struct {
	Label    string `json:"label"`
	Protocol string `json:"protocol"`
	Behavior string `json:"behavior"`
	Client   struct {
		Version int32           `json:"version"`
		Tag     int32           `json:"tag"`
		Payload json.RawMessage `json:"payload"`
	} `json:"client"`
	Server struct {
		Version              int32  `json:"version"`
		Tag                  int32  `json:"tag"`
		ID                   string `json:"id"`
		LastStreamIDReceived int32  `json:"last_stream_id_received"`
		ServerTimestamp      int64  `json:"server_timestamp"`
	} `json:"server"`
	ServerPing struct {
		Tag     int32           `json:"tag"`
		Payload json.RawMessage `json:"payload"`
	} `json:"server_ping"`
	ClientAck struct {
		Tag     int32           `json:"tag"`
		Payload json.RawMessage `json:"payload"`
	} `json:"client_ack"`
}

func loadFCMLoginTranscript(t *testing.T) mcsLoginTranscript {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "replay", "fixtures", "mcs", "synthetic", "fcm-login.json"))
	require.NoError(t, err)

	var transcript mcsLoginTranscript

	require.NoError(t, json.Unmarshal(data, &transcript))

	return transcript
}

func syntheticTLSCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	require.NoError(t, err)

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		DNSNames:     []string{protocol.MCSHost},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	require.NoError(t, err)

	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(parsed)

	return certificate, roots
}

func readMCSFrame(reader io.Reader) (int32, int32, []byte, error) {
	var prefix [2]byte

	_, err := io.ReadFull(reader, prefix[:])
	if err != nil {
		return 0, 0, nil, err
	}

	length, err := readMCSVarint(reader)
	if err != nil {
		return int32(prefix[0]), int32(prefix[1]), nil, err
	}

	payload := make([]byte, length)

	_, err = io.ReadFull(reader, payload)
	if err != nil {
		return int32(prefix[0]), int32(prefix[1]), nil, err
	}

	return int32(prefix[0]), int32(prefix[1]), payload, nil
}

func readMCSVarint(reader io.Reader) (uint64, error) {
	var encoded uint64

	for shift := uint(0); shift < 64; shift += 7 {
		var next [1]byte

		_, err := io.ReadFull(reader, next[:])
		if err != nil {
			return 0, err
		}

		encoded |= uint64(next[0]&0x7f) << shift
		if next[0] < 0x80 {
			return encoded, nil
		}
	}

	return 0, protowire.ParseError(-1)
}
