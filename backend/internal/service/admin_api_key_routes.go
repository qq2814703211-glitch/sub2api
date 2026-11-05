package service

import (
	"context"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
)

func (s *adminServiceImpl) AdminUpdateAPIKeyGroupRoutes(ctx context.Context, id int64, primary *int64, ids []int64) (*AdminUpdateAPIKeyGroupIDResult, error) {
	routes, first, err := normalizeAPIKeyGroupIDs(primary, ids)
	if err != nil {
		return nil, err
	}
	key, err := s.apiKeyRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if s.entClient == nil {
		return nil, fmt.Errorf("transaction client unavailable")
	}
	tx, err := s.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	opCtx := dbent.NewTxContext(ctx, tx)
	user, err := s.userRepo.GetByID(opCtx, key.UserID)
	if err != nil {
		return nil, err
	}
	result := &AdminUpdateAPIKeyGroupIDResult{}
	for _, gid := range routes {
		group, err := s.groupRepo.GetByID(opCtx, gid)
		if err != nil {
			return nil, err
		}
		if group == nil || !group.IsActive() {
			return nil, ErrGroupNotAllowed
		}
		if group.IsSubscriptionType() {
			routed := cloneAPIKeyWithGroupID(key, group)
			if _, err := resolveAPIKeyRouteSubscription(opCtx, s.userSubRepo, routed, nil); err != nil {
				return nil, err
			}
		} else if !user.CanBindGroup(gid, group.IsExclusive) {
			if err := s.userRepo.AddGroupToAllowedGroups(opCtx, user.ID, gid); err != nil {
				return nil, err
			}
			result.AutoGrantedGroupAccess = true
			grant := gid
			result.GrantedGroupID = &grant
			result.GrantedGroupName = group.Name
		}
		if first != nil && gid == *first {
			key.Group = group
		}
	}
	key.GroupID, key.RouteGroupIDs = first, routes
	if first == nil {
		key.Group = nil
	}
	if err := s.apiKeyRepo.Update(opCtx, key, APIKeyUpdateFields{GroupID: true, RouteGroupIDs: true}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if s.authCacheInvalidator != nil {
		s.authCacheInvalidator.InvalidateAuthCacheByUserID(ctx, key.UserID)
	}
	result.APIKey = key
	return result, nil
}
