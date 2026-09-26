package ring

import (
	"errors"

	"github.com/portpowered/go-ring/internal/signaling"
	"github.com/portpowered/go-ring/pkg/ringapimodels"
)

func sessionError(message string, err error) error {
	if err == nil {
		return nil
	}
	if ringapimodels.IsClosedError(err) || ringapimodels.IsConnectionError(err) || ringapimodels.IsBadRequestError(err) {
		return err
	}
	if errors.Is(err, signaling.ErrClosed) {
		return ringapimodels.NewClosedError(message, err)
	}
	return ringapimodels.NewConnectionError(message, err)
}
