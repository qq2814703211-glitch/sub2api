package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/poison"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// attachPoisonGuard 在账号选中后挂载投毒检测。
//
// 调用时机必须早于任何响应字节写出；这里放在 account 确定之后、转发之前。
// 名称前缀、上游白名单前缀和代理绑定三个条件在 poison 包内判定，不满足时
// 不做任何包装，请求开销与改动前一致。
func attachPoisonGuard(c *gin.Context, account *service.Account) {
	if c == nil || account == nil {
		return
	}
	info := poison.AccountInfo{
		Name:             account.Name,
		BaseURL:          account.GetCredential("base_url"),
		HasProxy:         account.ProxyID != nil,
		AccountID:        account.ID,
		ProxyID:          account.ProxyID,
		RestoreEpoch:     poison.RestoreEpoch(account.Extra),
		SharedGeneration: poison.SharedGeneration(account.Extra),
	}
	// 代理详情已内联在账号上时直接取用，出口 IP 判定要用到 host/port。
	if account.Proxy != nil {
		info.ProxyHost = account.Proxy.Host
		info.ProxyPort = account.Proxy.Port
	}
	poison.AttachFromAccount(c, info)
}
