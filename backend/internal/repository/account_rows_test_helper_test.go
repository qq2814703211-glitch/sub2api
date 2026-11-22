package repository

import (
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// updatedAccountRows builds the sqlmock result set for an accounts UPDATE ...
// RETURNING projection. extra is the raw JSONB payload of the updated row.
func updatedAccountRows(id int64, extra string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows(dbaccount.Columns).AddRow(
		id, now, now, nil, "test", nil, service.PlatformOpenAI, service.AccountTypeAPIKey,
		[]byte(`{"api_key":"sk-test"}`), []byte(extra), nil, nil, 1, nil, 1, 1.0,
		service.StatusActive, nil, nil, nil, false, true, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, service.QuotaDimensionGlobal,
	)
}
