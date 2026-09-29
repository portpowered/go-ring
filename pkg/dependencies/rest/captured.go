package rest

import (
	"context"

	"github.com/portpowered/go-ring/internal/generatedhttp"
	"github.com/portpowered/go-ring/internal/ringerrors"
)

// GetDeviceDetail returns the captured v3 detail envelope.
func (c *Client) GetDeviceDetail(ctx context.Context, id int64) (*generatedhttp.DeviceDetail, error) {
	var result generatedhttp.DeviceDetail

	req, err := generatedhttp.NewGetDeviceRequest(generatedServerBase(c.baseURI), id)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build device detail request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) ListLocations(ctx context.Context) (*generatedhttp.LocationList, error) {
	var result generatedhttp.LocationList

	req, err := generatedhttp.NewListLocationsRequest(generatedServerBase(c.baseURI))
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build locations request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) GetLocation(
	ctx context.Context,
	id string,
	params generatedhttp.GetLocationParams,
) (*generatedhttp.LocationDetail, error) {
	var result generatedhttp.LocationDetail

	req, err := generatedhttp.NewGetLocationRequest(generatedServerBase(c.baseURI), id, &params)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build location request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) ListLocationGroups(ctx context.Context, id string) (*generatedhttp.LocationGroups, error) {
	var result generatedhttp.LocationGroups

	req, err := generatedhttp.NewListLocationGroupsRequest(generatedServerBase(c.baseURI), id)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build location groups request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) ListLocationDevices(ctx context.Context, id string) (*generatedhttp.LocationGroupDevices, error) {
	var result generatedhttp.LocationGroupDevices

	req, err := generatedhttp.NewListLocationDevicesRequest(generatedServerBase(c.baseURI), id)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build location devices request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) GetDeviceTimeline(
	ctx context.Context,
	id int64,
	params generatedhttp.GetDeviceTimelineParams,
) (*generatedhttp.DeviceTimeline, error) {
	var result generatedhttp.DeviceTimeline

	req, err := generatedhttp.NewGetDeviceTimelineRequest(generatedServerBase(c.baseURI), id, &params)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build device timeline request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) GetHistoryDevices(
	ctx context.Context,
	params generatedhttp.GetHistoryDevicesParams,
) (*generatedhttp.HistoryDevices, error) {
	var result generatedhttp.HistoryDevices

	req, err := generatedhttp.NewGetHistoryDevicesRequest(generatedServerBase(c.baseURI), &params)
	if err != nil {
		return nil, ringerrors.NewNetworkError("failed to build history devices request", err)
	}

	err = c.doGeneratedJSON(ctx, req, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) RebootDevice(ctx context.Context, id int64) error {
	command := generatedhttp.DeviceCommand{CommandName: generatedhttp.Reboot}

	req, err := generatedhttp.NewSendDeviceCommandRequest(generatedServerBase(c.baseURI), id, command)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build reboot request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

// UnlockIntercom sends the documented device RPC to an intercom.
func (c *Client) UnlockIntercom(ctx context.Context, id int64) error {
	command := generatedhttp.IntercomUnlockCommand{
		CommandName: generatedhttp.DeviceRpc,
		Request: generatedhttp.IntercomUnlockRPC{
			Jsonrpc: generatedhttp.N20,
			Method:  generatedhttp.UnlockDoor,
			Params: generatedhttp.IntercomUnlockParams{
				DoorId: generatedhttp.IntercomUnlockParamsDoorIdN0,
				UserId: generatedhttp.IntercomUnlockParamsUserIdN0,
			},
		},
	}

	req, err := generatedhttp.NewUnlockIntercomRequest(generatedServerBase(c.baseURI), id, command)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build intercom unlock request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

func (c *Client) SetPersistentLiveViewEnabled(ctx context.Context, id int64, enabled bool) error {
	body := generatedhttp.LiveViewSettingRequest{Entity: generatedhttp.LiveViewSetting{LiveViewEnabled: enabled}}

	req, err := generatedhttp.NewSetLiveViewEnabledRequest(generatedServerBase(c.baseURI), id, body)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build live view settings request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

func (c *Client) FavoriteRecording(ctx context.Context, id int64) error {
	req, err := generatedhttp.NewFavoriteRecordingRequest(generatedServerBase(c.baseURI), id)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build favorite recording request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}

func (c *Client) DeleteRecording(ctx context.Context, id int64, confirmFavorite *bool) error {
	params := &generatedhttp.DeleteRecordingParams{ConfirmDeleteFavorite: confirmFavorite}

	req, err := generatedhttp.NewDeleteRecordingRequest(generatedServerBase(c.baseURI), id, params)
	if err != nil {
		return ringerrors.NewNetworkError("failed to build delete recording request", err)
	}

	return c.doGeneratedJSON(ctx, req, nil)
}
