package rest

import (
	"context"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// RegisterPushDevice binds an FCM token to the authenticated Ring account.
func (c *Client) RegisterPushDevice(ctx context.Context, token string) error {
	model := deviceModel
	version := int(generatedhttp.N11)
	body := generatedhttp.PushDeviceRegistration{Device: generatedhttp.PushDeviceRegistrationDevice{
		Metadata: generatedhttp.PushDeviceMetadata{
			ApiVersion: &version, DeviceModel: &model,
			PnDictVersion: generatedhttp.N200, PnService: generatedhttp.Fcm,
		},
		Os:                    generatedhttp.PushDeviceRegistrationDeviceOsAndroid,
		PushNotificationToken: token,
	}}

	req, err := generatedhttp.NewRegisterPushDeviceRequest(generatedServerBase(c.baseURI), body)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build push registration request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

func (c *Client) SubscribeDeviceDing(ctx context.Context, id int64) error {
	req, err := generatedhttp.NewSubscribeDeviceDingRequest(generatedServerBase(c.baseURI), id)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build ding subscription request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

func (c *Client) SubscribeDeviceMotion(ctx context.Context, id int64) error {
	req, err := generatedhttp.NewSubscribeDeviceMotionRequest(generatedServerBase(c.baseURI), id)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build motion subscription request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}
