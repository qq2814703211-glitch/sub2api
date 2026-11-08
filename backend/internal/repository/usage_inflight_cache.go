package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// 在途请求登记表（请求级快照）。
//
//   - usage:inflight:{uid}        HASH  field=requestID value=JSON 快照
//   - usage:inflight_group:{gid}  HASH  field=userID    value=该用户在该分组的在途请求数
//
// 与 concurrency:user:{id}（只有计数）和 billing:inflight:{uid}（只有金额）不同，
// 这里存的是请求级明细：model / group / started_at。管理面「谁正在用、用什么模型、
// 在哪个分组」以及分组容量里的 active_users 都从这里读。
//
// 两个 key 都不续期：TTL 跟随最长请求时长（沿用并发槽位 TTL），过期即自然消失。
// 分组 key 用「用户 → 在途数」的引用计数，而不是把 userID 直接塞进有序集合：
// 同一个用户在同一分组有多个并发请求时，只有最后一个结束才能让 COUNT(DISTINCT user)
// 减一，引用计数能在 HINCRBY/HDEL 的原子性下做到这一点，HLEN 就是精确去重人数。
//
// 写路径全部是 best-effort：调用方（中间件）失败只记日志，不阻断业务请求。
const (
	usageInflightKeyPrefix      = "usage:inflight:"
	usageInflightGroupKeyPrefix = "usage:inflight_group:"

	// 默认最长请求时长（分钟），与并发槽位 TTL 的默认值保持一致。
	defaultUsageInflightTTLMinutes = 15
)

func usageInflightKey(userID int64) string {
	return fmt.Sprintf("%s%d", usageInflightKeyPrefix, userID)
}

func usageInflightGroupKey(groupID int64) string {
	return fmt.Sprintf("%s%d", usageInflightGroupKeyPrefix, groupID)
}

type usageInflightCache struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewUsageInflightCache 创建在途请求登记表。
// ttlMinutes 为最长请求时长（分钟），0 或负数使用默认值。
func NewUsageInflightCache(rdb *redis.Client, ttlMinutes int) service.UsageInflightStore {
	if ttlMinutes <= 0 {
		ttlMinutes = defaultUsageInflightTTLMinutes
	}
	return &usageInflightCache{rdb: rdb, ttl: time.Duration(ttlMinutes) * time.Minute}
}

// ProvideUsageInflightCache 从网关配置读取最长请求时长，保持与并发槽位 TTL 同源。
func ProvideUsageInflightCache(rdb *redis.Client, cfg *config.Config) service.UsageInflightStore {
	ttlMinutes := defaultUsageInflightTTLMinutes
	if cfg != nil && cfg.Gateway.ConcurrencySlotTTLMinutes > 0 {
		ttlMinutes = cfg.Gateway.ConcurrencySlotTTLMinutes
	}
	return NewUsageInflightCache(rdb, ttlMinutes)
}

func (c *usageInflightCache) usable() bool {
	return c != nil && c.rdb != nil
}

// Register 登记一条在途请求，并为所属分组累加该用户的在途引用计数。
func (c *usageInflightCache) Register(ctx context.Context, snap service.UsageInflightSnapshot) error {
	if !c.usable() || snap.UserID <= 0 || snap.RequestID == "" {
		return nil
	}
	payload, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal usage inflight snapshot: %w", err)
	}
	userKey := usageInflightKey(snap.UserID)
	pipe := c.rdb.Pipeline()
	pipe.HSet(ctx, userKey, snap.RequestID, payload)
	pipe.PExpire(ctx, userKey, c.ttl)
	if snap.GroupID > 0 {
		groupKey := usageInflightGroupKey(snap.GroupID)
		pipe.HIncrBy(ctx, groupKey, strconv.FormatInt(snap.UserID, 10), 1)
		pipe.PExpire(ctx, groupKey, c.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("register usage inflight: %w", err)
	}
	return nil
}

// UpdateModel 补写在途快照的 model / stream。
//
// 登记发生在鉴权之后、请求体解析之前，那时还不知道模型名；handler 解析出模型后
// 通过 UpdateUsageInflightModel 补写。快照字段已消失（请求结束或 TTL 到期）时不重建，
// 避免给已注销的请求留下幽灵条目。
func (c *usageInflightCache) UpdateModel(ctx context.Context, userID int64, requestID, model string, stream bool) error {
	if !c.usable() || userID <= 0 || requestID == "" {
		return nil
	}
	userKey := usageInflightKey(userID)
	raw, err := c.rdb.HGet(ctx, userKey, requestID).Result()
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return fmt.Errorf("read usage inflight before model update: %w", err)
	}
	var snap service.UsageInflightSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return fmt.Errorf("unmarshal usage inflight snapshot: %w", err)
	}
	snap.Model = model
	snap.Stream = stream
	payload, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal usage inflight snapshot: %w", err)
	}
	if err := c.rdb.HSet(ctx, userKey, requestID, payload).Err(); err != nil {
		return fmt.Errorf("update usage inflight model: %w", err)
	}
	return nil
}

// Release 注销在途请求；只有在快照确实存在过时才递减分组引用计数，
// 重复注销（请求结束 + 重试）不会把计数减成负数。
func (c *usageInflightCache) Release(ctx context.Context, userID int64, requestID string, groupID int64) error {
	if !c.usable() || userID <= 0 || requestID == "" {
		return nil
	}
	removed, err := c.rdb.HDel(ctx, usageInflightKey(userID), requestID).Result()
	if err != nil {
		return fmt.Errorf("release usage inflight: %w", err)
	}
	if removed == 0 || groupID <= 0 {
		return nil
	}
	groupKey := usageInflightGroupKey(groupID)
	field := strconv.FormatInt(userID, 10)
	left, err := c.rdb.HIncrBy(ctx, groupKey, field, -1).Result()
	if err != nil {
		return fmt.Errorf("decrement usage inflight group refcount: %w", err)
	}
	if left > 0 {
		return nil
	}
	if err := c.rdb.HDel(ctx, groupKey, field).Err(); err != nil {
		return fmt.Errorf("drop usage inflight group refcount: %w", err)
	}
	return nil
}

// ListByUser 读取某个用户的全部在途快照。
func (c *usageInflightCache) ListByUser(ctx context.Context, userID int64) ([]service.UsageInflightSnapshot, error) {
	if !c.usable() || userID <= 0 {
		return nil, nil
	}
	raw, err := c.rdb.HGetAll(ctx, usageInflightKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("read usage inflight: %w", err)
	}
	snapshots := make([]service.UsageInflightSnapshot, 0, len(raw))
	for _, value := range raw {
		var snap service.UsageInflightSnapshot
		if err := json.Unmarshal([]byte(value), &snap); err != nil {
			// 单条脏数据不应该让整张登记表读不出来。
			continue
		}
		snapshots = append(snapshots, snap)
	}
	return snapshots, nil
}

// CountByUsers 批量返回一批用户的在途请求数。
//
// 计数口径刻意与 ListByUser 保持一致：只统计能成功反序列化的快照，
// 否则列表（这里）和详情（SummarizeUserInflight 走 ListByUser）会对同一个
// 用户给出不同的数。用 Pipeline 一次往返取全部 HASH，避免按用户逐个往返。
func (c *usageInflightCache) CountByUsers(ctx context.Context, userIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(userIDs))
	if !c.usable() || len(userIDs) == 0 {
		return counts, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(userIDs))
	queried := make([]int64, len(userIDs))
	for i, userID := range userIDs {
		if userID <= 0 {
			continue
		}
		queried[i] = userID
		cmds[i] = pipe.HGetAll(ctx, usageInflightKey(userID))
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("read usage inflight counts: %w", err)
	}
	for i, cmd := range cmds {
		if queried[i] <= 0 || cmd == nil {
			continue
		}
		raw, err := cmd.Result()
		if err != nil {
			return nil, fmt.Errorf("read usage inflight count for user %d: %w", queried[i], err)
		}
		n := 0
		for _, value := range raw {
			var snap service.UsageInflightSnapshot
			if err := json.Unmarshal([]byte(value), &snap); err != nil {
				// 与 ListByUser 一致：单条脏数据不计入，也不让整批读失败。
				continue
			}
			n++
		}
		counts[queried[i]] = n
	}
	return counts, nil
}

// ListByGroup 读取分组内当前有在途请求的全部快照（跨用户）。
//
// 分组 key 只保存 userID → 在途数的引用计数，明细仍在各自的 usage:inflight:{uid}
// 里，因此这里先取该分组的用户列表，再用一次 Pipeline 批量读回明细并按 group_id
// 过滤（请求过程中分组可能被改写，不能只信分组 key）。
func (c *usageInflightCache) ListByGroup(ctx context.Context, groupID int64) ([]service.UsageInflightSnapshot, error) {
	if !c.usable() || groupID <= 0 {
		return nil, nil
	}
	fields, err := c.rdb.HKeys(ctx, usageInflightGroupKey(groupID)).Result()
	if err != nil {
		return nil, fmt.Errorf("read usage inflight group users: %w", err)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	pipe := c.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, 0, len(fields))
	for _, field := range fields {
		userID, err := strconv.ParseInt(field, 10, 64)
		if err != nil || userID <= 0 {
			continue
		}
		cmds = append(cmds, pipe.HGetAll(ctx, usageInflightKey(userID)))
	}
	if len(cmds) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("read usage inflight by group: %w", err)
	}
	snapshots := make([]service.UsageInflightSnapshot, 0, len(cmds))
	for _, cmd := range cmds {
		for _, value := range cmd.Val() {
			var snap service.UsageInflightSnapshot
			if err := json.Unmarshal([]byte(value), &snap); err != nil {
				continue
			}
			if snap.GroupID != groupID {
				continue
			}
			snapshots = append(snapshots, snap)
		}
	}
	return snapshots, nil
}

// CountActiveUsersByGroups 批量返回每个分组当前实时使用中的用户数（COUNT(DISTINCT user_id)）。
func (c *usageInflightCache) CountActiveUsersByGroups(ctx context.Context, groupIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(groupIDs))
	if !c.usable() || len(groupIDs) == 0 {
		return counts, nil
	}
	pipe := c.rdb.Pipeline()
	type groupCmd struct {
		groupID int64
		cmd     *redis.IntCmd
	}
	cmds := make([]groupCmd, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		cmds = append(cmds, groupCmd{groupID: groupID, cmd: pipe.HLen(ctx, usageInflightGroupKey(groupID))})
	}
	if len(cmds) == 0 {
		return counts, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("count usage inflight group users: %w", err)
	}
	for _, item := range cmds {
		counts[item.groupID] = int(item.cmd.Val())
	}
	return counts, nil
}

var _ service.UsageInflightStore = (*usageInflightCache)(nil)
