package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func ProvideAccountOpsHandler(s *service.AccountOpsService, e *service.EmailService, t *service.TotpService, u *service.UserService, settings *service.SettingService) *admin.AccountOpsHandler {
	h := admin.NewAccountOpsHandler(s, e)
	h.SetStepUpDependencies(t, u, settings)
	return h
}
