package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/cleanup"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Read the current observation, including a settings result that completed
// during the upstream refresh. The write compares this exact JSON again.
func (r *accountRepository) grokSubscriptionRotation(ctx context.Context, id int64, expected map[string]any, proxyID *int64, credentials map[string]any) (prior []byte, replacement any, retErr error) {
	rows, err := r.sql.QueryContext(ctx, `SELECT COALESCE(a.extra->'grok_subscription_snapshot','null'::jsonb),
 CASE WHEN p.id IS NULL THEN 'null'::jsonb ELSE jsonb_build_object('id',p.id,'protocol',p.protocol,'host',p.host,'port',p.port,'username',COALESCE(p.username,''),'password',COALESCE(p.password,'')) END
 FROM accounts a LEFT JOIN proxies p ON p.id=a.proxy_id AND p.deleted_at IS NULL
 WHERE a.id=$1 AND a.deleted_at IS NULL`, id)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup.Close(&retErr, rows)
	if !rows.Next() {
		return []byte("null"), nil, rows.Err()
	}
	var previous, proxyJSON []byte
	if err := rows.Scan(&previous, &proxyJSON); err != nil {
		return nil, nil, err
	}
	var raw any
	if err := json.Unmarshal(previous, &raw); err != nil {
		return nil, nil, err
	}
	a := &service.Account{ID: id, Platform: service.PlatformGrok, Type: service.AccountTypeOAuth, Credentials: expected, ProxyID: proxyID, Extra: map[string]any{"grok_subscription_snapshot": raw}}
	if err := json.Unmarshal(proxyJSON, &a.Proxy); err != nil {
		return nil, nil, err
	}
	migrated := service.GrokSubscriptionAfterTokenRotation(a, credentials)
	if migrated == nil {
		return previous, nil, nil
	}
	next, err := json.Marshal(migrated)
	if err != nil {
		return nil, nil, err
	}
	return previous, string(next), nil
}
