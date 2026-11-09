//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestUpdateAccountScanArityMatchesSelect 钉死「账号设置保存」这条路径上的
// SELECT / Scan 参数个数匹配。
//
// 背景：清理上游探测功能时删掉了三个 probe SELECT 列和三处 extra 回填，
// 但漏删了 SELECT 里那个「Ollama 云配额可用性」布尔列 —— 它当时的消费者
// 已被一并删除。结果是 SELECT 返回 14 列、Scan 只接 13 个目标，
// 每次保存账号设置都返回 500：
//
//	[ERROR] PUT /api/v1/admin/accounts/<id>
//	Error: sql: expected 14 destination arguments in Scan, not 13
//
// 这条链路只在 integration tag 下才有真实 Postgres，普通 go test 编译不到，
// 所以它一路进了生产。任何对 lockAndMergeAccountProbeExtra 的 SELECT/Scan
// 改动都必须让本用例保持绿。
func TestUpdateAccountScanArityMatchesSelect(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)

	account := mustCreateAccount(t, tx.Client(), &service.Account{
		Name:     "scan-arity-probe",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "scan-arity-key",
			"base_url": "https://api.example.test/v1",
		},
	})

	// 直接走 Update：它就是 PUT /admin/accounts/:id 落到仓储层的那条路径，
	// 内部会执行 lockAndMergeAccountProbeExtra 的 SELECT + rows.Scan。
	renamed := "scan-arity-probe-renamed"
	account.Name = renamed
	require.NoError(t, repo.Update(ctx, account),
		"保存账号设置不应失败：SELECT 列数与 Scan 目标数必须一致")

	loaded, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, renamed, loaded.Name)
}

// TestUpdateOllamaAccountPreservesManagedExtraAcrossSave 覆盖「保存设置不改
// 身份时受管 extra 被回填」的行为，顺带证明被删掉的那个布尔列确实没人用。
//
// 组身份（platform/type/credentials/proxy）不变时，Ollama 会话与刷新开关
// 必须从锁定行回填，不能被表单里的旧值或缺失值冲掉。
func TestUpdateOllamaAccountPreservesManagedExtraAcrossSave(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)

	account := mustCreateAccount(t, tx.Client(), &service.Account{
		Name:     "ollama-managed-extra",
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "ollama-managed-key",
			"base_url": "https://ollama.com",
		},
		Extra: map[string]any{
			service.OllamaCloudUsageSessionExtraKey:     "cipher:wos-session=fixture",
			service.OllamaCloudUsageAutoRefreshExtraKey: true,
		},
	})

	// 只改名字，身份字段（credentials / platform / type / proxy）都不变。
	account.Name = "ollama-managed-extra-renamed"
	account.Extra = map[string]any{} // 模拟前端 DTO 不回传受管字段
	require.NoError(t, repo.Update(ctx, account))

	loaded, err := repo.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "cipher:wos-session=fixture",
		loaded.Extra[service.OllamaCloudUsageSessionExtraKey],
		"身份未变时应从锁定行回填 Ollama 会话")
	require.Equal(t, true, loaded.Extra[service.OllamaCloudUsageAutoRefreshExtraKey],
		"身份未变时应从锁定行回填刷新开关")
}
