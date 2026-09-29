/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"

	"github.com/portpowered/go-ring/internal/generatedfcm"
	"github.com/portpowered/go-ring/internal/protocol"
	pb "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/checkin"
	"google.golang.org/protobuf/proto"
)

type checkInOption struct {
	androidID     uint64
	securityToken uint64
}

const checkInVersion int32 = 3

type gcmRegisterResponse struct {
	token         string
	androidID     uint64
	securityToken uint64
}

func (c *Client) registerGCM(ctx context.Context) (*gcmRegisterResponse, error) {
	checkInResp, err := c.checkIn(ctx, &checkInOption{androidID: 0, securityToken: 0})
	if err != nil {
		return nil, err
	}

	if checkInResp.AndroidId == nil || checkInResp.SecurityToken == nil {
		return nil, terminalError("check-in response did not contain device credentials")
	}

	return c.doRegister(ctx, checkInResp.GetAndroidId(), checkInResp.GetSecurityToken())
}

func (c *Client) checkIn(ctx context.Context, opt *checkInOption) (*pb.AndroidCheckinResponse, error) {
	if opt.androidID > math.MaxInt64 {
		return nil, terminalError("Android ID exceeds the check-in protocol range")
	}

	id := int64(opt.androidID)
	request := &pb.AndroidCheckinRequest{
		Checkin: &pb.AndroidCheckinProto{
			ChromeBuild: &pb.ChromeBuildProto{
				Platform:      pb.ChromeBuildProto_PLATFORM_LINUX.Enum(),
				ChromeVersion: proto.String(chromeVersion),
				Channel:       pb.ChromeBuildProto_CHANNEL_STABLE.Enum(),
			},
			Type:       pb.DeviceType_DEVICE_CHROME_BROWSER.Enum(),
			UserNumber: proto.Int32(0),
		},
		Fragment:         proto.Int32(0),
		Version:          proto.Int32(checkInVersion),
		UserSerialNumber: proto.Int32(0),
		Id:               &id,
		SecurityToken:    &opt.securityToken,
	}

	message, err := proto.Marshal(request)
	if err != nil {
		return nil, wrapError(err, "marshal GCM checkin request")
	}

	httpRequest, err := generatedfcm.NewCheckInFCMClientRequestWithBody(
		"https://"+protocol.FCMCheckinHost,
		&generatedfcm.CheckInFCMClientParams{
			ContentType: generatedfcm.CheckInFCMClientParamsContentTypeApplicationxProtobuf,
		},
		string(generatedfcm.CheckInFCMClientParamsContentTypeApplicationxProtobuf),
		bytes.NewReader(message),
	)
	if err != nil {
		return nil, wrapError(err, "create GCM checkin request")
	}

	res, err := c.post(ctx, httpRequest)
	if err != nil {
		return nil, wrapError(err, "request GCM checkin")
	}
	defer closeResponse(res)

	// unauthorized error
	if res.StatusCode == http.StatusUnauthorized {
		return nil, ErrGcmAuthorization
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, terminalError("server error: " + res.Status)
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, wrapError(err, "read GCM checkin response")
	}

	var responseProto pb.AndroidCheckinResponse

	err = proto.Unmarshal(data, &responseProto)
	if err != nil {
		return nil, wrapError(err, "unmarshal GCM checkin response")
	}

	return &responseProto, nil
}

func (c *Client) doRegister(ctx context.Context, androidID uint64, securityToken uint64) (*gcmRegisterResponse, error) {
	authToken := "AidLogin " + strconv.FormatUint(androidID, 10) + ":" + strconv.FormatUint(securityToken, 10)

	request, err := generatedfcm.NewRegisterFCMClientRequestWithFormdataBody(
		"https://"+protocol.FCMRegisterHost,
		&generatedfcm.RegisterFCMClientParams{
			ContentType:   generatedfcm.ApplicationxWwwFormUrlencoded,
			Authorization: authToken,
			UserAgent:     "",
		},
		generatedfcm.LegacyRegistrationRequest{
			App:      generatedfcm.OrgChromiumLinux,
			XSubtype: generatedfcm.LegacyRegistrationRequestXSubtype(c.appID),
			Device:   strconv.FormatUint(androidID, 10),
			Sender:   generatedfcm.LegacyRegistrationRequestSender(c.vapidKey),
		},
	)
	if err != nil {
		return nil, wrapError(err, "create GCM register request")
	}

	res, err := c.post(ctx, request)
	if err != nil {
		return nil, wrapError(err, "request GCM register")
	}
	defer closeResponse(res)

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, wrapError(err, "read GCM register response")
	}

	subscription, err := url.ParseQuery(string(data))
	if err != nil {
		return nil, wrapError(err, "parse GCM register URL")
	}

	token := subscription.Get("token")

	return &gcmRegisterResponse{
		token:         token,
		androidID:     androidID,
		securityToken: securityToken,
	}, nil
}
