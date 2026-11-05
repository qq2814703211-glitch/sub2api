package main

import (
	"context"
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestDeleteBeforeDeletesInBatchesUntilExhausted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	before := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	query := `DELETE FROM group_rate_history\s+WHERE id IN \(\s*SELECT id FROM group_rate_history\s+WHERE changed_at < \$1\s+ORDER BY id\s+LIMIT \$2\s*\)`
	mock.ExpectExec(query).
		WithArgs(before, 2).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(query).
		WithArgs(before, 2).
		WillReturnResult(sqlmock.NewResult(0, 1))

	deleted, err := deleteBefore(context.Background(), db, before, 2)
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteBeforePropagatesErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec(`DELETE FROM group_rate_history`).WillReturnError(errors.New("boom"))

	deleted, err := deleteBefore(context.Background(), db, time.Now(), 10)
	require.EqualError(t, err, "boom")
	require.Zero(t, deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCountBeforeUsesUTCAndReportsRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	zone := time.FixedZone("UTC+8", 8*3600)
	before := time.Date(2026, 8, 1, 8, 0, 0, 0, zone)
	mock.ExpectQuery(`SELECT COUNT\(\*\) FROM group_rate_history WHERE changed_at < \$1`).
		WithArgs(before.UTC()).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(7)))

	count, err := countBefore(context.Background(), db, before)
	require.NoError(t, err)
	require.Equal(t, int64(7), count)
	require.NoError(t, mock.ExpectationsWereMet())
}
