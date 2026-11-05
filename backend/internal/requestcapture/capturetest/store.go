// Package capturetest supplies synthetic metadata storage for producer tests.
package capturetest

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/requestcapture"
	"sort"
	"sync"
)

type Task = requestcapture.Task
type Record = requestcapture.Record
type MemoryStore struct {
	mu      sync.Mutex
	tasks   map[string][]byte
	records map[string][]byte
}

func NewStore() *MemoryStore {
	return &MemoryStore{tasks: map[string][]byte{}, records: map[string][]byte{}}
}
func (s *MemoryStore) SaveTask(_ context.Context, t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(t)
	s.tasks[t.ID] = b
	return err
}
func (s *MemoryStore) Task(_ context.Context, instance, id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.tasks[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var t Task
	err := json.Unmarshal(b, &t)
	if t.InstanceID != instance {
		return nil, sql.ErrNoRows
	}
	return &t, err
}
func (s *MemoryStore) Tasks(_ context.Context, instance string, limit, offset int) ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Task{}
	for _, b := range s.tasks {
		var t Task
		_ = json.Unmarshal(b, &t)
		if t.InstanceID == instance {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset >= len(out) {
		return []Task{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}
func (s *MemoryStore) SaveRecord(_ context.Context, r *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[r.TaskID]; !ok {
		return sql.ErrNoRows
	}
	b, err := json.Marshal(r)
	s.records[r.ID] = b
	return err
}
func (s *MemoryStore) Record(_ context.Context, task, id string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.records[id]
	if !ok {
		return nil, sql.ErrNoRows
	}
	var r Record
	err := json.Unmarshal(b, &r)
	if r.TaskID != task {
		return nil, sql.ErrNoRows
	}
	return &r, err
}
func (s *MemoryStore) Records(_ context.Context, task, rid string, errorsOnly bool, limit, offset int) ([]Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Record{}
	for _, b := range s.records {
		var r Record
		_ = json.Unmarshal(b, &r)
		if r.TaskID == task && (rid == "" || r.RequestID == rid || r.ClientRequestID == rid || r.SessionID == rid || r.ThreadID == rid) && (!errorsOnly || (r.IsError && r.FinishedAt != nil)) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if offset >= len(out) {
		return []Record{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}
func (s *MemoryStore) DeleteRecord(_ context.Context, task, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if raw, ok := s.records[id]; ok {
		var r Record
		_ = json.Unmarshal(raw, &r)
		if r.TaskID == task {
			delete(s.records, id)
		}
	}
	return nil
}
func (s *MemoryStore) DeleteTask(_ context.Context, instance, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
	for key, b := range s.records {
		var r Record
		_ = json.Unmarshal(b, &r)
		if r.TaskID == id {
			delete(s.records, key)
		}
	}
	return nil
}
