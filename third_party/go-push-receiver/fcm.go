/*
 * Copyright (c) 2019 Zenichi Amano
 *
 * This file is part of go-push-receiver, which is MIT licensed.
 * See http://opensource.org/licenses/MIT
 */

package pushreceiver

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/portpowered/go-ring/internal/generatedfcm"
	"github.com/portpowered/go-ring/internal/protocol"
	"google.golang.org/protobuf/proto"

	pb "github.com/portpowered/go-ring/third_party/go-push-receiver/pb/mcs"
)

const (
	fidBytesLength    = 17
	fidHeader         = 0b01110000
	fidRandomBitsMask = 0b00010000
)

// FCMCredentials is the credentials returned by FCM.
type FCMCredentials struct {
	AppID         string `json:"appId"`
	AndroidID     uint64 `json:"androidId"`
	Endpoint      string `json:"endpoint"`
	SecurityToken uint64 `json:"securityToken"`
	Token         string `json:"token"`
	PrivateKey    []byte `json:"privateKey"`
	PublicKey     []byte `json:"publicKey"`
	AuthSecret    []byte `json:"authSecret"`
}

// Subscribe to FCM.
func (c *Client) Subscribe(ctx context.Context) {
	defer close(c.Events)

	for ctx.Err() == nil {
		var err error
		if c.creds == nil {
			err = c.register(ctx)
		} else {
			_, err = c.checkIn(ctx, &checkInOption{c.creds.AndroidID, c.creds.SecurityToken})
		}

		if err == nil {
			// reset retry count when connection success
			c.backoff.reset()

			err = c.tryToConnect(ctx)
		}

		if err != nil {
			if errors.Is(err, ErrGcmAuthorization) {
				c.Events <- &UnauthorizedError{err}

				c.creds = nil
			}

			if c.retryDisabled {
				return
			}
			// retry
			sleepDuration := c.backoff.duration()
			c.Events <- &RetryEvent{err, sleepDuration}

			tick := time.After(sleepDuration)
			select {
			case <-tick:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (c *Client) register(ctx context.Context) error {
	register, err := c.registerGCM(ctx)
	if err != nil {
		return err
	}

	install, err := c.installFCM(ctx)
	if err != nil {
		return err
	}

	creds, err := c.registerFCM(ctx, register, install)
	if err != nil {
		return err
	}

	c.creds = creds
	c.Events <- &UpdateCredentialsEvent{creds}

	return nil
}

func (c *Client) tryToConnect(ctx context.Context) (returnErr error) {
	childCtx, cancelChild := context.WithCancel(ctx)
	defer cancelChild()

	var conn net.Conn

	var err error

	if c.mcsDialContext != nil {
		conn, err = c.mcsDialContext(ctx, protocol.MCSNetwork, protocol.MCSAddress)
	} else {
		dialer := &tls.Dialer{NetDialer: c.dialer, Config: c.tlsConfig}
		conn, err = dialer.DialContext(ctx, protocol.MCSNetwork, protocol.MCSAddress)
	}

	if err != nil {
		return wrapError(err, "dial failed to FCM")
	}

	defer func() {
		closeErr := conn.Close()
		if closeErr != nil {
			returnErr = errors.Join(returnErr, wrapError(closeErr, "close MCS connection"))
		}
	}()

	mcs := c.newMCS(conn)
	defer mcs.disconnect("disconnect")

	err = mcs.sendLoginPacket(childCtx, c.receivedPersistentID)
	if err != nil {
		return wrapError(err, "send login packet failed")
	}

	// start heartbeat
	go c.heartbeat.start(
		childCtx,
		c.logger,
		mcs.heartbeatAck,
		func() error {
			return mcs.sendHeartbeatPingPacket(childCtx)
		},
		func() {
			mcs.disconnect("heartbeat")
			cancelChild()
		})

	select {
	case err := <-c.asyncPerformRead(childCtx, mcs):
		return err
	case <-childCtx.Done():
		return wrapError(childCtx.Err(), "MCS read cancelled")
	}
}

func (c *Client) asyncPerformRead(ctx context.Context, mcs *mcs) <-chan error {
	ch := make(chan error, 1)

	go func() {
		defer close(ch)

		ch <- c.performRead(ctx, mcs)
	}()

	return ch
}

func (c *Client) performRead(ctx context.Context, mcs *mcs) error {
	// receive version
	err := mcs.receiveVersion(ctx)
	if err != nil {
		return wrapError(err, "receive version failed")
	}

	for {
		// receive tag
		data, err := mcs.performReadTag(ctx)
		if err != nil {
			return wrapError(err, "receive tag failed")
		}

		if data == nil || data.message == nil {
			return ErrFcmNotEnoughData
		}

		err = c.onDataMessage(data.message)
		if err != nil {
			return wrapError(err, "process data message failed")
		}
	}
}

func (c *Client) onDataMessage(tagData proto.Message) error {
	switch data := tagData.(type) {
	case *pb.LoginResponse:
		c.receivedPersistentID = nil
		c.Events <- &ConnectedEvent{data.GetServerTimestamp()}
	case *pb.DataMessageStanza:
		// To avoid error loops, last streamId is notified even when an error occurs.
		c.receivedPersistentID = append(c.receivedPersistentID, data.GetPersistentId())

		event, err := decryptData(data, c.creds)
		if err != nil {
			return err
		}

		c.Events <- event
	}

	return nil
}

func (c *Client) installFCM(ctx context.Context) (*generatedfcm.InstallationResponse, error) {
	fid, err := generateFID()
	if err != nil {
		return nil, err
	}

	// refs. https://github.com/firebase/firebase-js-sdk/blob/main/packages/installations/src/util/constants.ts#L22
	generatedBody := generatedfcm.InstallationRequest{
		AppId:       generatedfcm.InstallationRequestAppId(c.appID),
		AuthVersion: generatedfcm.FISV2,
		Fid:         fid,
		SdkVersion:  generatedfcm.W0617,
	}
	params := &generatedfcm.CreateFCMInstallationParams{
		Accept:      generatedfcm.CreateFCMInstallationParamsAcceptApplicationjson,
		ContentType: generatedfcm.CreateFCMInstallationParamsContentTypeApplicationjson,
		XGoogApiKey: generatedfcm.CreateFCMInstallationParamsXGoogApiKey(c.apiKey),
	}

	request, err := generatedfcm.NewCreateFCMInstallationRequest(
		"https://"+protocol.FCMInstallationsHost,
		params,
		generatedBody,
	)
	if err != nil {
		return nil, wrapError(err, "create FCM install request")
	}

	res, err := c.post(ctx, request)
	if err != nil {
		return nil, wrapError(err, "request FCM install")
	}
	defer closeResponse(res)

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, terminalError("server error: " + res.Status)
	}

	var fcmInstallResponse generatedfcm.InstallationResponse

	err = json.NewDecoder(res.Body).Decode(&fcmInstallResponse)
	if err != nil {
		return nil, wrapError(err, "unmarshal FCM install response")
	}

	return &fcmInstallResponse, nil
}

func (c *Client) registerFCM(
	ctx context.Context,
	registerResponse *gcmRegisterResponse,
	installResponse *generatedfcm.InstallationResponse,
) (*FCMCredentials, error) {
	credentials := new(FCMCredentials)

	err := credentials.appendCryptoInfo()
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf(fcmLegacyEndpoint, registerResponse.token)
	body := generatedfcm.FCMRegistrationRequest{
		Web: generatedfcm.FCMWebRegistration{
			Endpoint: endpoint,
			P256dh:   base64.URLEncoding.EncodeToString(credentials.PublicKey),
			Auth:     base64.URLEncoding.EncodeToString(credentials.AuthSecret),
		},
	}

	params := &generatedfcm.RegisterFCMInstallationParams{
		ContentType:                    generatedfcm.RegisterFCMInstallationParamsContentTypeApplicationjson,
		XGoogApiKey:                    generatedfcm.RegisterFCMInstallationParamsXGoogApiKey(c.apiKey),
		XGoogFirebaseInstallationsAuth: installResponse.AuthToken.Token,
	}

	request, err := generatedfcm.NewRegisterFCMInstallationRequest(
		"https://"+protocol.FCMRegistrationsHost,
		params,
		body,
	)
	if err != nil {
		return nil, wrapError(err, "create FCM register request")
	}

	res, err := c.post(ctx, request)
	if err != nil {
		return nil, wrapError(err, "request FCM register")
	}
	defer closeResponse(res)

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, terminalError("server error: " + res.Status)
	}

	var fcmRegisterResponse generatedfcm.FCMRegistrationResponse

	err = json.NewDecoder(res.Body).Decode(&fcmRegisterResponse)
	if err != nil {
		return nil, wrapError(err, "unmarshal FCM register response")
	}

	// set responses.
	credentials.AppID = c.appID
	credentials.AndroidID = registerResponse.androidID
	credentials.SecurityToken = registerResponse.securityToken
	credentials.Token = fcmRegisterResponse.Token
	credentials.Endpoint = endpoint

	return credentials, nil
}

func generateFID() (string, error) {
	// refs. https://github.com/firebase/firebase-js-sdk/blob/main/packages/installations/src/helpers/generate-fid.ts
	// A valid FID has exactly 22 base64 characters, which is 132 bits, or 16.5
	// bytes. our implementation generates a 17 byte array instead.
	fid := make([]byte, fidBytesLength)

	_, err := rand.Read(fid)
	if err != nil {
		return "", wrapError(err, "generate FID bytes")
	}

	// Replace the first four random bits with the constant FID header.
	fid[0] = fidHeader | (fid[0] % fidRandomBitsMask)

	return base64.StdEncoding.EncodeToString(fid), nil
}
