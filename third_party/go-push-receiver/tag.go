/*
 * Copyright (c) 2025 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"fmt"

	"github.com/portpowered/go-ring/internal/protocol"
	pb "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
	"google.golang.org/protobuf/proto"
)

// tagType is the FCM request or response tag type.
type tagType byte

// Tag enumeration.
const (
	tagHeartbeatPing       tagType = protocol.MCSHeartbeatPingTag
	tagHeartbeatAck        tagType = protocol.MCSHeartbeatAckTag
	tagLoginRequest        tagType = protocol.MCSLoginRequestTag
	tagLoginResponse       tagType = protocol.MCSLoginResponseTag
	tagClose               tagType = protocol.MCSCloseTag
	tagMessageStanza       tagType = protocol.MCSMessageStanzaTag
	tagPresenceStanza      tagType = protocol.MCSPresenceStanzaTag
	tagIqStanza            tagType = protocol.MCSIqStanzaTag
	tagDataMessageStanza   tagType = protocol.MCSDataMessageStanzaTag
	tagBatchPresenceStanza tagType = protocol.MCSBatchPresenceStanzaTag
	tagStreamErrorStanza   tagType = protocol.MCSStreamErrorStanzaTag
	tagHTTPRequest         tagType = protocol.MCSHTTPRequestTag
	tagHTTPResponse        tagType = protocol.MCSHTTPResponseTag
	tagBindAccountRequest  tagType = protocol.MCSBindAccountRequestTag
	tagBindAccountResponse tagType = protocol.MCSBindAccountResponseTag
	tagTalkMetadata        tagType = protocol.MCSTalkMetadataTag
	tagNumProtoTypes       tagType = protocol.MCSNumProtoTypesTag
	tagUnknown             tagType = protocol.MCSUnknownTag
)

func (t tagType) String() string {
	switch t {
	case tagHeartbeatPing:
		return "HeartbeatPing(0)"
	case tagHeartbeatAck:
		return "HeartbeatAck(1)"
	case tagLoginRequest:
		return "LoginRequest(2)"
	case tagLoginResponse:
		return "LoginResponse(3)"
	case tagClose:
		return "Close(4)"
	case tagMessageStanza:
		return "MessageStanza(5)"
	case tagPresenceStanza:
		return "PresenceStanza(6)"
	case tagIqStanza:
		return "IqStanza(7)"
	case tagDataMessageStanza:
		return "DataMessageStanza(8)"
	case tagBatchPresenceStanza:
		return "BatchPresenceStanza(9)"
	case tagStreamErrorStanza:
		return "StreamErrorStanza(10)"
	case tagHTTPRequest:
		return "HTTPRequest(11)"
	case tagHTTPResponse:
		return "HTTPResponse(12)"
	case tagBindAccountRequest:
		return "BindAccountRequest(13)"
	case tagBindAccountResponse:
		return "BindAccountResponse(14)"
	case tagTalkMetadata:
		return "TalkMetadata(15)"
	case tagNumProtoTypes:
		return "NumProtoTypes(16)"
	case tagUnknown:
		return fmt.Sprintf("Unknown(%d)", t)
	default:
		return fmt.Sprintf("Unknown(%d)", t)
	}
}

// GenerateMessage returns the protobuf message associated with the tag.
func (t tagType) GenerateMessage() proto.Message {
	switch t {
	case tagHeartbeatPing:
		return new(pb.HeartbeatPing)
	case tagHeartbeatAck:
		return new(pb.HeartbeatAck)
	case tagLoginRequest:
		return new(pb.LoginRequest)
	case tagLoginResponse:
		return new(pb.LoginResponse)
	case tagClose:
		return new(pb.Close)
	case tagIqStanza:
		return new(pb.IqStanza)
	case tagDataMessageStanza:
		return new(pb.DataMessageStanza)
	case tagStreamErrorStanza:
		return new(pb.StreamErrorStanza)
	case tagMessageStanza,
		tagPresenceStanza,
		tagBatchPresenceStanza,
		tagHTTPRequest,
		tagHTTPResponse,
		tagBindAccountRequest,
		tagBindAccountResponse,
		tagTalkMetadata,
		tagNumProtoTypes,
		tagUnknown:
		return nil
	default:
		return nil
	}
}
