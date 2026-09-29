package pushreceiver

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/go-ring/internal/ringerrors"
	pb "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type heartbeatPair struct {
	Tag     byte            `json:"tag"`
	Payload json.RawMessage `json:"payload"`
}

type heartbeatTranscript struct {
	ClientPing heartbeatPair `json:"client_ping"`
	ServerAck  heartbeatPair `json:"server_ack"`
}

func TestMCSHeartbeatFramesMatchPairedTranscript(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "tests", "replay", "fixtures", "mcs", "synthetic", "fcm-login.json")
	// #nosec G304 -- the path is a fixed checked-in synthetic replay fixture.
	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var transcript heartbeatTranscript

	err = json.Unmarshal(raw, &transcript)
	require.NoError(t, err)

	client, peer := net.Pipe()

	defer func() { require.NoError(t, client.Close()) }()
	defer func() { require.NoError(t, peer.Close()) }()

	stream := &mcs{
		conn:             client,
		logger:           slog.New(slog.DiscardHandler),
		creds:            nil,
		incomingStreamId: 0,
		heartbeatAck:     make(chan bool, 1),
		heartbeat:        nil,
		disconnectDm:     sync.Once{},
		events:           nil,
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	sent := make(chan error, 1)

	go func() { sent <- stream.sendHeartbeatPingPacket(ctx) }()

	tag, payload, err := readPairedMCSTagFrame(peer)
	require.NoError(t, err)
	require.Equal(t, transcript.ClientPing.Tag, tag)

	actualPing := new(pb.HeartbeatPing)
	err = proto.Unmarshal(payload, actualPing)
	require.NoError(t, err)

	expectedPing := new(pb.HeartbeatPing)
	err = protojson.Unmarshal(transcript.ClientPing.Payload, expectedPing)
	require.NoError(t, err)
	require.True(t, proto.Equal(expectedPing, actualPing), "outbound MCS ping differs from paired fixture")
	require.NoError(t, <-sent)

	consume := make(chan error, 1)

	go func() {
		_, receiveErr := stream.performReadTag(ctx)
		consume <- receiveErr
	}()

	ack := new(pb.HeartbeatAck)
	err = protojson.Unmarshal(transcript.ServerAck.Payload, ack)
	require.NoError(t, err)

	ackPayload, err := proto.Marshal(ack)
	require.NoError(t, err)

	frame := []byte{transcript.ServerAck.Tag}
	frame = protowire.AppendVarint(frame, uint64(len(ackPayload)))
	frame = append(frame, ackPayload...)

	_, err = peer.Write(frame)
	require.NoError(t, err)
	require.NoError(t, <-consume)
	require.Equal(t, ack.GetLastStreamIdReceived(), stream.incomingStreamId)
	require.True(t, <-stream.heartbeatAck)
}

func readPairedMCSTagFrame(reader io.Reader) (byte, []byte, error) {
	buffered := bufio.NewReader(reader)

	tag, err := buffered.ReadByte()
	if err != nil {
		return 0, nil, ringerrors.NewConnectionError("read MCS heartbeat tag", err)
	}

	length, err := binary.ReadUvarint(buffered)
	if err != nil {
		return tag, nil, ringerrors.NewBadRequestError("read MCS heartbeat length", err)
	}

	payload := make([]byte, length)

	_, err = io.ReadFull(buffered, payload)
	if err != nil {
		return tag, nil, ringerrors.NewConnectionError("read MCS heartbeat payload", err)
	}

	return tag, payload, nil
}
