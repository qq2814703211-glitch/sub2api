package handler

import (
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func isAPIKeyAdmissionError(err error) bool {
	var qe *service.APIKeyQueueError
	return errors.As(err, &qe) || errors.Is(err, service.ErrAPIKeyReservationLost) || errors.Is(err, service.ErrAPIKeySlotLeaseLost)
}
