package service

import "database/sql"

func ProvideUpstreamMonitorService(repo UpstreamMonitorRepository, lock LeaderLockCache, db *sql.DB) *UpstreamMonitorService {
	s := NewUpstreamMonitorService(repo)
	s.SetLeaderLock(lock, db)
	return s
}
