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

// GetMultiple returns the stored values for the requested keys, omitting
// missing ones — matching the real repository's behaviour that callers rely
// on (absent key = empty string in the returned map).
func (r *inMemorySettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}
