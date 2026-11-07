package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// channelMonitorV3Prober 是 ChannelMonitorV3Prober 的默认实现。
//
// 它复用 V1 已经验证过的探活链路（runCheckForModel：challenge 校验、SSRF 安全
// dial、TTFT 测量），但运行在 V3 自己的配置之上，并且不往 usage_logs 写任何东西 ——
// 探针目标就是运维在 V3 设置页填的上游 endpoint，与本站网关无关。
type channelMonitorV3Prober struct {
	encryptor SecretEncryptor
}

// NewChannelMonitorV3Prober 创建默认探针执行器。
func NewChannelMonitorV3Prober(encryptor SecretEncryptor) ChannelMonitorV3Prober {
	return &channelMonitorV3Prober{encryptor: encryptor}
}

// DecryptAPIKey 解开 V3 探针 key 的密文。
func (p *channelMonitorV3Prober) DecryptAPIKey(ciphertext string) (string, error) {
	ciphertext = strings.TrimSpace(ciphertext)
	if ciphertext == "" {
		return "", ErrChannelMonitorMissingAPIKey
	}
	if p.encryptor == nil {
		return "", errors.New("channel monitor v3: secret encryptor unavailable")
	}
	plain, err := p.encryptor.Decrypt(ciphertext)
	if err != nil {
		return "", fmt.Errorf("decrypt channel monitor v3 api key: %w", err)
	}
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return "", ErrChannelMonitorMissingAPIKey
	}
	return plain, nil
}

// ProbeGroup 对该分组并发跑一次 primary + extra 模型探测。
//
// 失败不返回 error：每个模型的失败都已经写进样本的 Success 字段，一条上游失败
// 必须体现为图上的一个红点，而不是让整轮探测中断。
func (p *channelMonitorV3Prober) ProbeGroup(
	ctx context.Context,
	probe ChannelMonitorV3Probe,
	groupID int64,
	models []string,
) []ChannelMonitorV3ProbeSample {
	if groupID <= 0 || len(models) == 0 {
		return nil
	}
	apiKey := probe.APIKeyEncrypted
	if apiKey == "" {
		return nil
	}
	opts := &CheckOptions{APIMode: MonitorAPIModeChatCompletions}
	results := make([]ChannelMonitorV3ProbeSample, len(models))
	var wg sync.WaitGroup
	for i, model := range models {
		i, model := i, model
		wg.Add(1)
		go func() {
			defer wg.Done()
			check := runCheckForModel(ctx, probe.Provider, probe.Endpoint, apiKey, model, opts)
			results[i] = ChannelMonitorV3ProbeSample{
				GroupID: groupID,
				// degraded 仍然算可用：上游回了正确内容，只是慢。
				Success:   check.Status == MonitorStatusOperational || check.Status == MonitorStatusDegraded,
				TTFTMs:    check.TtftMs,
				CheckedAt: check.CheckedAt.UTC(),
			}
		}()
	}
	wg.Wait()
	return results
}
