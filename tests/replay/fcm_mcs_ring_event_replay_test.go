package replay_test

import (
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

	"github.com/portpowered/go-ring/internal/generatedfcm"
	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/internal/ringerrors"
	"github.com/portpowered/go-ring/internal/testkit/replay"
	"github.com/portpowered/go-ring/pkg/ring"
	mcsPB "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

//nolint:wsl,wsl_v5 // The replay sequence mirrors the ordered request and response transcript.
func TestFCMEncryptedRingEventReplaysThroughInjectedMCSFrame(t *testing.T) {
	t.Parallel()

	message := replayFCMRingEvent(t)
	require.NoError(t, message.Err)
	transcript := loadFCMRingEventTranscript(t)
	var expected fcmExpectedMessage
	require.NoError(t, json.Unmarshal(transcript.ExpectedEvent, &expected))
	require.JSONEq(t, expected.Data, string(message.Data))
	assertFCMRingGeneratedSchema(t, message.Data, transcript.SchemaExpectation)

	parsed := ring.ParseFCMNotification(message.Data)
	require.Equal(t, ring.PushMessage, parsed.Kind)
	require.Equal(t, "123456", parsed.DeviceID)
	require.Equal(t, ring.PushAction("ding"), parsed.Action)
}

//nolint:wsl,wsl_v5 // The replay sequence mirrors the ordered request and response transcript.
func replayFCMRingEvent(t *testing.T) ring.FCMEvent {
	t.Helper()

	transcript := loadFCMRingEventTranscript(t)
	login := loadFCMReplayLoginTranscript(t)
	checkin, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "fcm", "checkin-existing.json"))
	require.NoError(t, err)

	transport := replay.NewTransport(checkin)
	ringExchange, err := replay.LoadExchange(filepath.Join("fixtures", "http", "synthetic", "ring-push-register.json"))
	require.NoError(t, err)
	ringTransport := replay.NewTransport(ringExchange)

	certificate, roots := replayMCSTLSCertificate(t)
	//nolint:exhaustruct // Unspecified TLS options intentionally use the safe library defaults.
	serverConfig := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13}
	//nolint:exhaustruct // Unspecified TLS options intentionally use the safe library defaults.
	clientConfig := &tls.Config{RootCAs: roots, ServerName: protocol.MCSHost, MinVersion: tls.VersionTLS13}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverResult := make(chan error, 1)
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		if network != protocol.MCSNetwork || address != net.JoinHostPort(protocol.MCSHost, protocol.MCSPort) {
			return nil, net.ErrClosed
		}

		clientPipe, serverPipe := net.Pipe()
		go serveFCMRingEventTranscript(ctx, tls.Server(serverPipe, serverConfig), login, transcript, serverResult)

		return tls.Client(clientPipe, clientConfig), nil
	}

	client, err := ring.NewClient(
		ring.WithHTTPClient(&http.Client{Transport: ringTransport}),
		ring.WithFCMHTTPTransport(transport),
		ring.WithFCMDialContext(dial),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	connection, err := client.ConnectPush(ctx, ring.ConnectPushRequest{
		Auth:        ring.AuthContext{AccessToken: "synthetic-access-token", HardwareID: ""},
		Credentials: transcript.Credentials,
		DeviceIDs:   nil,
		Ding:        false,
		Motion:      false,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = connection.Close() })

	message := waitForFCMRingMessage(t, ctx, connection.Events())

	require.NoError(t, connection.Close())

	select {
	case serverErr := <-serverResult:
		require.NoError(t, serverErr)
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic MCS peer did not finish the encrypted event exchange")
	}
	require.NoError(t, transport.AssertConsumed())
	require.NoError(t, ringTransport.AssertConsumed())

	return message
}

func waitForFCMRingMessage(t *testing.T, ctx context.Context, events <-chan ring.FCMEvent) ring.FCMEvent {
	t.Helper()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("FCM receiver closed before the encrypted Ring event")
			}

			switch event.Kind {
			case ring.PushMessage:
				return event
			case ring.PushRetry:
				t.Fatalf("FCM receiver rejected the synthetic MCS transcript: %v", event.Err)
			case ring.PushCredentials, ring.PushRegistered, ring.PushConnected, ring.PushClosed:
				continue
			}
		case <-ctx.Done():
			t.Fatal("encrypted Ring event replay timed out")
		}
	}
}

//nolint:wsl,wsl_v5 // Keep schema decode and field assertions together for the synthetic event.
func assertFCMRingGeneratedSchema(t *testing.T, data, expectation json.RawMessage) {
	t.Helper()

	var expected struct {
		AndroidConfig json.RawMessage `json:"android_config"`
		Data          json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(expectation, &expected))

	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &envelope))

	configJSON := expandFCMJSONString(t, envelope["android_config"])
	var config generatedfcm.RingPushNotificationConfig
	require.NoError(t, json.Unmarshal(configJSON, &config))
	require.NotNil(t, config.Category)
	require.JSONEq(t, string(expected.AndroidConfig), string(configJSON))
	require.Equal(t, "ding", *config.Category)

	payloadJSON := expandFCMJSONString(t, envelope["data"])
	var payload generatedfcm.RingPushNotificationPayload
	require.NoError(t, json.Unmarshal(payloadJSON, &payload))
	require.NotNil(t, payload.Device)
	require.NotNil(t, payload.Device.Id)
	require.NotNil(t, payload.GcmData)
	require.NotNil(t, payload.GcmData.Action)
	require.JSONEq(t, string(expected.Data), string(payloadJSON))
	require.EqualValues(t, 123456, *payload.Device.Id)
	require.Equal(t, "ding", *payload.GcmData.Action)
}

//nolint:wsl,wsl_v5 // The string-or-object normalization is one small decode decision.
func expandFCMJSONString(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()

	var encoded string
	err := json.Unmarshal(raw, &encoded)
	if err == nil {
		return []byte(encoded)
	}

	return raw
}

//nolint:wsl,wsl_v5 // The server writes each framed transcript step immediately after validating its pair.
func serveFCMRingEventTranscript(
	ctx context.Context,
	server *tls.Conn,
	login fcmReplayLoginTranscript,
	transcript fcmRingEventTranscript,
	result chan<- error,
) {
	defer func() { _ = server.Close() }()

	err := server.HandshakeContext(ctx)
	if err != nil {
		result <- err

		return
	}

	version, tag, payload, err := readReplayMCSFrame(server, true)
	if err != nil {
		result <- err

		return
	}
	if version != login.Client.Version || tag != login.Client.Tag {
		result <- io.ErrUnexpectedEOF

		return
	}
	err = matchReplayMCSLogin(payload, login.Client.Payload)
	if err != nil {
		result <- err

		return
	}

	loginResponse, err := replayMCSLoginResponse(login)
	if err != nil {
		result <- err

		return
	}
	err = writeReplayMCSFrame(server, login.Server.Version, login.Server.Tag, loginResponse)
	if err != nil {
		result <- err

		return
	}

	err = sendReplayHeartbeat(server, login)
	if err != nil {
		result <- err

		return
	}

	message := new(mcsPB.DataMessageStanza)
	err = protojson.Unmarshal(transcript.ServerMessage.Payload, message)
	if err != nil {
		result <- err

		return
	}
	encoded, err := proto.Marshal(message)
	if err != nil {
		result <- err

		return
	}

	result <- writeReplayMCSFrame(server, 0, transcript.ServerMessage.Tag, encoded)
}

//nolint:wsl,wsl_v5 // This small encoder maps the fixture's response fields directly to the protobuf.
func replayMCSLoginResponse(login fcmReplayLoginTranscript) ([]byte, error) {
	//nolint:exhaustruct // The transcript only specifies the negotiated login response fields.
	response := &mcsPB.LoginResponse{
		Id:                   proto.String(login.Server.ID),
		LastStreamIdReceived: proto.Int32(login.Server.LastStreamIDReceived),
		ServerTimestamp:      proto.Int64(login.Server.ServerTimestamp),
	}
	encoded, err := proto.Marshal(response)
	if err != nil {
		return nil, ringerrors.NewBadRequestError("encode MCS login response", err)
	}

	return encoded, nil
}

//nolint:wsl,wsl_v5 // Heartbeat send and acknowledgment are an ordered MCS transcript pair.
func sendReplayHeartbeat(server io.ReadWriter, login fcmReplayLoginTranscript) error {
	ping := new(mcsPB.HeartbeatPing)
	err := protojson.Unmarshal(login.ServerPing.Payload, ping)
	if err != nil {
		return ringerrors.NewBadRequestError("decode synthetic heartbeat ping", err)
	}

	pingPayload, err := proto.Marshal(ping)
	if err != nil {
		return ringerrors.NewBadRequestError("encode synthetic heartbeat ping", err)
	}
	err = writeReplayMCSFrame(server, 0, login.ServerPing.Tag, pingPayload)
	if err != nil {
		return ringerrors.NewConnectionError("send synthetic heartbeat ping", err)
	}

	tag, ackPayload, err := readReplayMCSTagFrame(server)
	if err != nil {
		return ringerrors.NewConnectionError("read synthetic heartbeat ack", err)
	}

	ack := new(mcsPB.HeartbeatAck)
	err = proto.Unmarshal(ackPayload, ack)
	if err != nil {
		return ringerrors.NewBadRequestError("decode synthetic heartbeat ack", err)
	}

	expected := new(mcsPB.HeartbeatAck)
	err = protojson.Unmarshal(login.ClientAck.Payload, expected)
	if err != nil {
		return ringerrors.NewBadRequestError("decode expected heartbeat ack", err)
	}
	if tag != login.ClientAck.Tag || !proto.Equal(ack, expected) {
		return io.ErrUnexpectedEOF
	}

	return nil
}

//nolint:wsl,wsl_v5 // Decode and compare the paired login payload in one focused helper.
func matchReplayMCSLogin(payload []byte, expectedJSON json.RawMessage) error {
	actual := new(mcsPB.LoginRequest)
	err := proto.Unmarshal(payload, actual)
	if err != nil {
		return ringerrors.NewBadRequestError("decode MCS login request", err)
	}

	expected := new(mcsPB.LoginRequest)
	err = protojson.Unmarshal(expectedJSON, expected)
	if err != nil {
		return ringerrors.NewBadRequestError("decode expected MCS login request", err)
	}
	if !proto.Equal(actual, expected) {
		return io.ErrUnexpectedEOF
	}

	return nil
}

//nolint:wsl,wsl_v5 // MCS frame fields are appended in wire order.
func writeReplayMCSFrame(writer io.Writer, version, tag int32, payload []byte) error {
	frame := make([]byte, 0, len(payload)+8)
	if version > 0 {
		frame = append(frame, byte(version))
	}
	frame = append(frame, byte(tag))
	frame = protowire.AppendVarint(frame, uint64(len(payload)))
	frame = append(frame, payload...)

	_, err := writer.Write(frame)

	if err != nil {
		return ringerrors.NewConnectionError("write MCS frame", err)
	}

	return nil
}

//nolint:wsl,wsl_v5 // MCS frame fields are read in wire order.
func readReplayMCSFrame(reader io.Reader, hasVersion bool) (int32, int32, []byte, error) {
	var prefix [2]byte
	var err error
	if hasVersion {
		_, err = io.ReadFull(reader, prefix[:])
	} else {
		_, err = io.ReadFull(reader, prefix[1:])
	}

	if err != nil {
		return 0, 0, nil, ringerrors.NewConnectionError("read MCS frame header", err)
	}

	tag := prefix[1]
	length, err := readReplayMCSVarint(reader)
	if err != nil {
		return int32(prefix[0]), int32(tag), nil, ringerrors.NewConnectionError("read MCS frame length", err)
	}

	payload := make([]byte, length)
	_, err = io.ReadFull(reader, payload)
	if err != nil {
		return int32(prefix[0]), int32(tag), nil, ringerrors.NewConnectionError("read MCS frame payload", err)
	}

	return int32(prefix[0]), int32(tag), payload, nil
}

func readReplayMCSTagFrame(reader io.Reader) (int32, []byte, error) {
	_, tag, payload, err := readReplayMCSFrame(reader, false)

	return tag, payload, err
}

//nolint:wsl,wsl_v5 // The varint loop keeps its byte read, accumulation, and termination check together.
func readReplayMCSVarint(reader io.Reader) (uint64, error) {
	var encoded uint64

	for shift := uint(0); shift < 64; shift += 7 {
		var next [1]byte
		_, err := io.ReadFull(reader, next[:])
		if err != nil {
			return 0, ringerrors.NewConnectionError("read MCS varint", err)
		}
		encoded |= uint64(next[0]&0x7f) << shift
		if next[0] < 0x80 {
			return encoded, nil
		}
	}

	return 0, ringerrors.NewBadRequestError("parse MCS frame length", protowire.ParseError(-1))
}

//nolint:wsl // Fixture decoding and its identity checks form one setup operation.
func loadFCMRingEventTranscript(t *testing.T) fcmRingEventTranscript {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("fixtures", "mcs", "synthetic", "fcm-ring-event.json"))
	require.NoError(t, err)

	var transcript fcmRingEventTranscript
	require.NoError(t, json.Unmarshal(data, &transcript))
	require.Equal(t, "synthetic", transcript.Label)
	require.Equal(t, "MCS", transcript.Protocol)
	require.Equal(t, "encrypted_ring_event", transcript.Behavior)
	require.Equal(t, int32(protocol.MCSDataMessageStanzaTag), transcript.ServerMessage.Tag)

	return transcript
}

//nolint:wsl // Fixture decoding and its identity checks form one setup operation.
func loadFCMReplayLoginTranscript(t *testing.T) fcmReplayLoginTranscript {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("fixtures", "mcs", "synthetic", "fcm-login.json"))
	require.NoError(t, err)

	var login fcmReplayLoginTranscript
	require.NoError(t, json.Unmarshal(data, &login))

	return login
}

func replayMCSTLSCertificate(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	require.NoError(t, err)

	now := time.Now()
	//nolint:exhaustruct // The synthetic certificate sets only fields relevant to local TLS verification.
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

	//nolint:exhaustruct // Optional certificate fields are not needed by the loopback TLS peer.
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: privateKey}
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(parsed)

	return certificate, roots
}

type fcmReplayLoginTranscript struct {
	Client     fcmReplayClientFrame   `json:"client"`
	Server     fcmReplayLoginResponse `json:"server"`
	ServerPing fcmReplayTagPayload    `json:"server_ping"`
	ClientAck  fcmReplayTagPayload    `json:"client_ack"`
}

type fcmReplayClientFrame struct {
	Version int32           `json:"version"`
	Tag     int32           `json:"tag"`
	Payload json.RawMessage `json:"payload"`
}

type fcmReplayLoginResponse struct {
	Version              int32  `json:"version"`
	Tag                  int32  `json:"tag"`
	ID                   string `json:"id"`
	LastStreamIDReceived int32  `json:"last_stream_id_received"`
	ServerTimestamp      int64  `json:"server_timestamp"`
}

type fcmReplayTagPayload struct {
	Tag     int32           `json:"tag"`
	Payload json.RawMessage `json:"payload"`
}

type fcmRingEventTranscript struct {
	Label             string              `json:"label"`
	Protocol          string              `json:"protocol"`
	Behavior          string              `json:"behavior"`
	Credentials       json.RawMessage     `json:"credentials"`
	ServerMessage     fcmReplayTagPayload `json:"server_message"`
	ExpectedEvent     json.RawMessage     `json:"expected_event"`
	SchemaExpectation json.RawMessage     `json:"schema_expectation"`
}

type fcmExpectedMessage struct {
	Data string `json:"data"`
}
