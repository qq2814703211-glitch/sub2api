package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// An unresolved tariff is not a settled usage log. Keep the sanitized meters
// in the existing Ops error store under the original request and owner.
func (r *usageLogRepository) RecordTariffIssue(ctx context.Context, entry *service.OpsInsertErrorLogInput) error {
	_, err := NewOpsRepository(r.db).InsertErrorLog(ctx, entry)
	return err
}
