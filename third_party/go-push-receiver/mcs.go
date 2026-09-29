/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"

	pb "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const mcsAccountID int64 = 1000000

type mcs struct {
	conn             net.Conn
	logger           *slog.Logger
	creds            *FCMCredentials
	incomingStreamId int32
	heartbeatAck     chan bool
	heartbeat        *Heartbeat
	disconnectDm     sync.Once
	events           chan Event
}

type mcsPayload struct {
	message proto.Message
}

func (c *Client) newMCS(conn net.Conn) *mcs {
	return &mcs{
		conn:             conn,
		logger:           c.logger,
		creds:            c.creds,
		incomingStreamId: 0,
		heartbeatAck:     make(chan bool),
		heartbeat:        c.heartbeat,
		disconnectDm:     sync.Once{},
		events:           c.Events,
	}
}

func (mcs *mcs) disconnect(reason string) {
	mcs.disconnectDm.Do(func() {
		close(mcs.heartbeatAck)

		mcs.events <- &DisconnectedEvent{Reason: reason}
	})
}

func (mcs *mcs) sendLoginPacket(ctx context.Context, receivedPersistentID []string) error {
	androidID := proto.String(strconv.FormatUint(mcs.creds.AndroidID, 10))

	setting := []*pb.Setting{
		{
			Name:  proto.String("new_vc"),
			Value: proto.String("1"),
		},
	}

	if mcs.heartbeat.serverInterval > 0 {
		setting = append(setting, &pb.Setting{
			Name:  proto.String("hbping"),
			Value: proto.String(strconv.FormatInt(mcs.heartbeat.serverInterval.Milliseconds(), 10)),
		})
	}

	request := &pb.LoginRequest{
		AccountId:            proto.Int64(mcsAccountID),
		AuthService:          pb.LoginRequest_ANDROID_ID.Enum(),
		AuthToken:            proto.String(strconv.FormatUint(mcs.creds.SecurityToken, 10)),
		Id:                   proto.String("chrome-" + chromeVersion),
		Domain:               proto.String(mcsDomain),
		DeviceId:             proto.String("android-" + strconv.FormatUint(mcs.creds.AndroidID, 16)),
		NetworkType:          proto.Int32(1), // Wi-Fi
		Resource:             androidID,
		User:                 androidID,
		UseRmq2:              proto.Bool(true),
		LastRmqId:            proto.Int64(1), // Sending not enabled yet so this stays as 1.
		Setting:              setting,
		AdaptiveHeartbeat:    proto.Bool(mcs.heartbeat.adaptive),
		ReceivedPersistentId: receivedPersistentID,
	}

	return mcs.sendRequest(ctx, tagLoginRequest, request, true)
}

func (mcs *mcs) sendHeartbeatPingPacket(ctx context.Context) error {
	request := &pb.HeartbeatPing{
		LastStreamIdReceived: proto.Int32(mcs.incomingStreamId),
	}

	return mcs.sendRequest(ctx, tagHeartbeatPing, request, false)
}

func (mcs *mcs) sendHeartbeatAckPacket(ctx context.Context) error {
	request := &pb.HeartbeatAck{
		LastStreamIdReceived: proto.Int32(mcs.incomingStreamId),
	}

	return mcs.sendRequest(ctx, tagHeartbeatAck, request, false)
}

func (mcs *mcs) receiveVersion(ctx context.Context) error {
	ctxErr := ctx.Err()
	if ctxErr != nil {
		return wrapError(ctxErr, "receive version cancelled")
	}

	buf := make([]byte, versionPacketLen)

	length, err := io.ReadFull(mcs.conn, buf)
	if err != nil {
		return wrapError(err, "receive version packet")
	}

	if length != versionPacketLen || buf[0] != fcmVersion {
		return terminalError(fmt.Sprintf("version does not match: received %d, expected %d", buf[0], fcmVersion))
	}

	return nil
}

func (mcs *mcs) performReadTag(ctx context.Context) (*mcsPayload, error) {
	tag, err := mcs.receiveTag(ctx)
	if err != nil {
		return nil, wrapError(err, "receive tag packet")
	}

	size, err := mcs.receiveSize(ctx)
	if err != nil {
		return nil, wrapError(err, "receive size packet")
	}

	buf := make([]byte, size)

	_, err = io.ReadFull(mcs.conn, buf)
	if err != nil {
		return nil, wrapError(err, "receive data packet")
	}

	return mcs.unmarshalTagData(ctx, tag, buf)
}

func (mcs *mcs) unmarshalTagData(ctx context.Context, tag tagType, buf []byte) (*mcsPayload, error) {
	receive := tag.GenerateMessage()
	if receive == nil {
		return nil, terminalError(fmt.Sprintf("unknown tag: %x", tag))
	}

	err := proto.Unmarshal(buf, receive)
	if err != nil {
		return nil, wrapError(err, fmt.Sprintf("unmarshal tag(%x) data", tag))
	}

	if mcs.logger.Enabled(ctx, slog.LevelDebug) {
		mcs.logger.DebugContext(ctx, "MCS receive", "tag", tag, "message", protojson.Format(receive))
	}

	err = mcs.handleTag(ctx, receive)
	if err != nil {
		return nil, wrapError(err, "handle MCS tag")
	}

	return &mcsPayload{message: receive}, nil
}

func (mcs *mcs) sendRequest(ctx context.Context, tag tagType, request proto.Message, containVersion bool) error {
	err := ctx.Err()
	if err != nil {
		return wrapError(err, "send MCS request cancelled")
	}

	header := make([]byte, 0, 100)
	if containVersion {
		header = append(header, fcmVersion, byte(tag))
	} else {
		header = append(header, byte(tag))
	}

	if mcs.logger.Enabled(ctx, slog.LevelDebug) {
		mcs.logger.DebugContext(ctx, "MCS request", "tag", tag, "message", protojson.Format(request))
	}

	messageSize := proto.Size(request)
	if messageSize < 0 {
		return terminalError("negative encoded MCS message size")
	}

	header = protowire.AppendVarint(header, uint64(messageSize))

	data, err := proto.Marshal(request)
	if err != nil {
		return wrapError(err, "encode protocol buffer data")
	}

	frame := make([]byte, len(header)+len(data))
	copy(frame, header)
	copy(frame[len(header):], data)

	for len(frame) > 0 {
		err = ctx.Err()
		if err != nil {
			return wrapError(err, "write MCS request cancelled")
		}

		written, err := mcs.conn.Write(frame)
		if err != nil {
			return wrapError(err, "write MCS request")
		}

		if written == 0 {
			return wrapError(io.ErrShortWrite, "write MCS request")
		}

		frame = frame[written:]
	}

	return nil
}

func (mcs *mcs) handleTag(ctx context.Context, receive proto.Message) error {
	switch message := receive.(type) {
	case *pb.HeartbeatPing:
		mcs.updateIncomingStreamID(message.GetLastStreamIdReceived())

		mcs.heartbeatAck <- true

		return mcs.sendHeartbeatAckPacket(ctx)
	case *pb.HeartbeatAck:
		mcs.updateIncomingStreamID(message.GetLastStreamIdReceived())

		mcs.heartbeatAck <- true
	case *pb.LoginResponse:
		mcs.updateIncomingStreamID(message.GetLastStreamIdReceived())
	case *pb.IqStanza:
		mcs.updateIncomingStreamID(message.GetLastStreamIdReceived())
	}

	return nil
}

func (mcs *mcs) updateIncomingStreamID(lastStreamIDReceived int32) {
	if lastStreamIDReceived > 0 {
		mcs.incomingStreamId = lastStreamIDReceived
	}
}

func (mcs *mcs) receiveTag(ctx context.Context) (tagType, error) {
	ctxErr := ctx.Err()
	if ctxErr != nil {
		return tagUnknown, wrapError(ctxErr, "receive MCS tag cancelled")
	}

	buf := make([]byte, tagPacketLen)

	bytesRead, err := io.ReadFull(mcs.conn, buf)
	if err != nil {
		return tagUnknown, wrapError(err, "read MCS tag")
	}

	if bytesRead == 0 {
		return tagUnknown, wrapError(io.ErrClosedPipe, "read MCS tag")
	}

	return tagType(buf[0]), nil
}

func (mcs *mcs) receiveSize(ctx context.Context) (uint64, error) {
	offset := 0
	buf := make([]byte, sizePacketLenMax)

	for {
		ctxErr := ctx.Err()
		if ctxErr != nil {
			return 0, wrapError(ctxErr, "receive MCS size cancelled")
		}

		if offset >= sizePacketLenMax {
			return 0, wrapError(io.ErrUnexpectedEOF, "read MCS size")
		}

		length, err := mcs.conn.Read(buf[offset : offset+1])
		if err != nil {
			return 0, wrapError(err, "read MCS size")
		}

		if length == 0 {
			return 0, wrapError(io.ErrNoProgress, "read MCS size")
		}

		offset += length

		value, consumed := protowire.ConsumeVarint(buf[:offset])

		if consumed > 0 {
			return value, nil
		}
	}
}
