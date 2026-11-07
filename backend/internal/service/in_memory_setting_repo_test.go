package service

import (
	"context"
	"sync"
)

// inMemorySettingRepo is the shared in-memory SettingRepository test double.
// Used by the usage-probe and rate-limit suites that need real settings
// round-trips without a database.
type inMemorySettingRepo struct {
	SettingRepository
	mu     sync.Mutex
	values map[string]string
}

func (r *inMemorySettingRepo) GetValue(_ context.Context, key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, ok := r.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (r *inMemorySettingRepo) Set(_ context.Context, key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}
