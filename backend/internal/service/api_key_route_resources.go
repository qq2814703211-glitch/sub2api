package service

import (
	"context"
	"errors"
	"sort"
)

// ResolveAPIKeyRouteGroup only accepts a key-bound group for server-owned resources.
func (s *OpenAIGatewayService) ResolveAPIKeyRouteGroup(ctx context.Context, key *APIKey, id int64) (*APIKey, error) {
	resolver := s.keyRouteResolver()
	for _, gid := range key.CandidateGroupIDs() {
		if gid == id {
			if resolver.groups == nil {
				return nil, ErrSchedulerCacheNotReady
			}
			g, err := resolver.groups(ctx, gid)
			if err != nil {
				return nil, err
			}
			if g == nil || g.ID != id {
				return nil, ErrGroupNotFound
			}
			return cloneAPIKeyWithGroupID(key, g), nil
		}
	}
	return nil, ErrGroupNotAllowed
}
func (s *GatewayService) ResolveAPIKeyRouteGroup(ctx context.Context, key *APIKey, id int64) (*APIKey, error) {
	resolver := s.keyRouteResolver()
	for _, gid := range key.CandidateGroupIDs() {
		if gid == id {
			if resolver.groups == nil {
				return nil, ErrSchedulerCacheNotReady
			}
			g, err := resolver.groups(ctx, gid)
			if err != nil {
				return nil, err
			}
			if g == nil || g.ID != id {
				return nil, ErrGroupNotFound
			}
			return cloneAPIKeyWithGroupID(key, g), nil
		}
	}
	return nil, ErrGroupNotAllowed
}
func (s *GeminiCachedContentService) ListForAPIKey(ctx context.Context, key *APIKey, beforeID int64, limit int) ([]*GeminiCachedContent, error) {
	var records []*GeminiCachedContent
	for _, id := range key.CandidateGroupIDs() {
		items, err := s.List(ctx, key.ID, id, beforeID, limit)
		if err != nil {
			return nil, err
		}
		records = append(records, items...)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID > records[j].ID })
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}
func (s *GeminiCachedContentService) ResolveForAPIKey(ctx context.Context, key *APIKey, name string) (*GeminiCachedContent, error) {
	for _, id := range key.CandidateGroupIDs() {
		record, err := s.Resolve(ctx, key.ID, &id, name)
		if err == nil {
			return record, nil
		}
		if !errors.Is(err, ErrGeminiCachedContentNotFound) {
			return nil, err
		}
	}
	return nil, ErrGeminiCachedContentNotFound
}
