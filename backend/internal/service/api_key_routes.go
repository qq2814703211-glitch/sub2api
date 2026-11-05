// Ordered API Key fallback routing. Adapted from upstream cdf13f1991c369dbc5a4585ad197c103f0772451.
package service

import (
	"context"
	"fmt"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// CandidateGroupIDs returns a detached, stable-order projection, including legacy keys.
func (k *APIKey) CandidateGroupIDs() []int64 {
	if k == nil {
		return nil
	}
	out := make([]int64, 0, len(k.RouteGroupIDs))
	seen := map[int64]bool{}
	for _, id := range k.RouteGroupIDs {
		if id > 0 && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	if len(out) == 0 && k.GroupID != nil && *k.GroupID > 0 {
		out = append(out, *k.GroupID)
	}
	return out
}
func (k *APIKey) HasMultipleRoutes() bool { return len(k.CandidateGroupIDs()) > 1 }

// The external contract deduplicates in order and rejects invalid identifiers.
func normalizeAPIKeyGroupIDs(primary *int64, ids []int64) ([]int64, *int64, error) {
	out := make([]int64, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 {
			return nil, nil, infraerrors.BadRequest("API_KEY_GROUP_ID_INVALID", "group_ids entries must be positive")
		}
		if !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	if len(out) == 0 {
		if primary != nil && *primary > 0 {
			out = append(out, *primary)
		} else {
			return out, nil, nil
		}
	}
	if primary != nil && *primary > 0 && *primary != out[0] {
		return nil, nil, infraerrors.BadRequest("API_KEY_GROUP_ID_MISMATCH", "group_id must match the first group_ids entry")
	}
	id := out[0]
	return out, &id, nil
}
func (s *APIKeyService) validateAPIKeyGroupRoutes(ctx context.Context, user *User, primary *int64, ids []int64) ([]int64, *int64, error) {
	routes, first, err := normalizeAPIKeyGroupIDs(primary, ids)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range routes {
		group, err := s.groupRepo.GetByID(ctx, id)
		if err != nil {
			return nil, nil, fmt.Errorf("get group: %w", err)
		}
		if group == nil || !group.IsActive() || !s.canUserBindGroup(ctx, user, group) {
			return nil, nil, ErrGroupNotAllowed
		}
	}
	return routes, first, nil
}
func cloneAPIKeyWithGroupID(key *APIKey, group *Group) *APIKey {
	if key == nil {
		return nil
	}
	clone := *key
	if group != nil {
		id := group.ID
		clone.GroupID, clone.Group = &id, group
	}
	return &clone
}
