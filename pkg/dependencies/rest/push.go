package rest

import (
	"context"
	"net/http"

	"github.com/portpowered/go-ring/internal/protocol"
	"github.com/portpowered/go-ring/pkg/generatedhttp"
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
	return c.doJSONRequest(ctx, http.MethodPatch, protocol.PushDeviceRegistrationPath, body, nil)
}

func (c *Client) SubscribeDeviceDing(ctx context.Context, id int64) error {
	return c.doJSONRequest(ctx, http.MethodPost, capturedIDPath(protocol.DeviceDingSubscribePath, id), nil, nil)
}

func (c *Client) SubscribeDeviceMotion(ctx context.Context, id int64) error {
	return c.doJSONRequest(ctx, http.MethodPost, capturedIDPath(protocol.DeviceMotionSubscribePath, id), nil, nil)
}
