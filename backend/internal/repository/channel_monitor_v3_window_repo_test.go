//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// L1-3：分钟窗口不能走日汇总表（它的最小粒度是一天），必须落到 usage_logs 直扫，
// 且时间范围精确等于窗口分钟数；天窗口继续走汇总表快路径，性能不回退。

func TestChannelMonitorV3MinuteWindowScansUsageLogsWithMinuteInterval(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &channelMonitorV3Repository{db: db}

	rows := sqlmock.NewRows([]string{"group_id", "cache_rate"}).AddRow(int64(7), 0.25)
	// 关键断言：参数是 30（分钟），不是 30（天），也不是 1（向上取整的天）。
	mock.ExpectQuery("FROM usage_logs").
		WithArgs(sqlmock.AnyArg(), 30).
		WillReturnRows(rows)

	rates, err := repo.GroupCacheRates(context.Background(), []int64{7}, 30)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{7: 0.25}, rates)
	require.NoError(t, mock.ExpectationsWereMet())
}

// SQL 里的时间窗必须是 minute 而不是 day：「30 分钟」被写成「30 天」是静默错答案。
func TestChannelMonitorV3CacheRateSQLEvaluatesTheWindowInMinutes(t *testing.T) {
	require.Contains(t, channelMonitorV3CacheRateWindowExpr, "INTERVAL '1 minute'")
	require.NotContains(t, channelMonitorV3CacheRateWindowExpr, "INTERVAL '1 day'")
}

// 天窗口仍然先试日汇总表：这是 0.1.1 的性能优化（19,714ms -> 0.3ms），不能为了
// 支持分钟而废掉。这里用一个可用的汇总表读侧证明快路径被走到。
func TestChannelMonitorV3DayWindowStillUsesDailyRollup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	reader := &stubGroupDailyReader{
		usable: true,
		rates:  map[int64]float64{7: 0.5},
	}
	repo := &channelMonitorV3Repository{db: db, groupDaily: reader}

	rates, err := repo.GroupCacheRates(context.Background(), []int64{7}, 30*24*60)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{7: 0.5}, rates)
	require.Equal(t, 1, reader.calls, "a whole-day window must use the daily rollup")
	require.Equal(t, 30, reader.windowDays, "the rollup reader takes whole days")
	// 走快路径时不该碰 usage_logs。
	require.NoError(t, mock.ExpectationsWereMet())
}

// 分钟窗口必须绕过汇总表，哪怕它「可用」。
func TestChannelMonitorV3MinuteWindowBypassesDailyRollup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	reader := &stubGroupDailyReader{
		usable: true,
		rates:  map[int64]float64{7: 0.5},
	}
	repo := &channelMonitorV3Repository{db: db, groupDaily: reader}

	mock.ExpectQuery("FROM usage_logs").
		WithArgs(sqlmock.AnyArg(), 30).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "cache_rate"}).AddRow(int64(7), 0.25))

	rates, err := repo.GroupCacheRates(context.Background(), []int64{7}, 30)
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{7: 0.25}, rates)
	require.Zero(t, reader.calls, "a sub-day window must not consult the daily rollup")
	require.NoError(t, mock.ExpectationsWereMet())
}

// 90 小时（5400 分钟）不是整天数：不能被悄悄取整成 4 天走汇总表。
func TestChannelMonitorV3NonWholeDayWindowBypassesDailyRollup(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	reader := &stubGroupDailyReader{usable: true, rates: map[int64]float64{7: 0.5}}
	repo := &channelMonitorV3Repository{db: db, groupDaily: reader}

	mock.ExpectQuery("FROM usage_logs").
		WithArgs(sqlmock.AnyArg(), 90*60).
		WillReturnRows(sqlmock.NewRows([]string{"group_id", "cache_rate"}))

	_, err = repo.GroupCacheRates(context.Background(), []int64{7}, 90*60)
	require.NoError(t, err)
	require.Zero(t, reader.calls, "90 hours is not a whole number of days")
	require.NoError(t, mock.ExpectationsWereMet())
}

// stubGroupDailyReader 是 UsageGroupDailyReader 的测试替身，记录被调用的窗口。
type stubGroupDailyReader struct {
	usable     bool
	rates      map[int64]float64
	calls      int
	windowDays int
}

func (r *stubGroupDailyReader) RollupUsable(context.Context) (bool, error) {
	return r.usable, nil
}

func (r *stubGroupDailyReader) CacheRates(
	_ context.Context,
	_ []int64,
	windowDays int,
	_ time.Time,
) (map[int64]float64, error) {
	r.calls++
	r.windowDays = windowDays
	return r.rates, nil
}

// 候选分组列表的过滤条件：已删除的分组不能出现在候选里。
//
// 生产数据里存在 status=active 但 deleted_at 有值的行（软删除时 status 没改），
// 所以 deleted_at IS NULL 是不可省的那一半。
func TestChannelMonitorV3MonitorableGroupQueryExcludesDeletedGroups(t *testing.T) {
	require.Contains(t, channelMonitorV3MonitorableGroupsQuery, "deleted_at IS NULL",
		"a soft-deleted group with status='active' must not be a candidate")
	require.Contains(t, channelMonitorV3MonitorableGroupsQuery, "status = 'active'",
		"a paused group must not be a candidate either")
}

var _ service.ChannelMonitorV3Repository = (*channelMonitorV3Repository)(nil)
