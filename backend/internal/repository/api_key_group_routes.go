package repository

import (
	"context"
	"entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/apikey"
	"github.com/Wei-Shaw/sub2api/ent/apikeygrouproute"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Key metadata and the complete ordered list commit or roll back together.
func (r *apiKeyRepository) routeTransaction(ctx context.Context, fn func(*apiKeyRepository) error) error {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return fn(&apiKeyRepository{client: tx.Client(), sql: r.sql})
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(&apiKeyRepository{client: tx.Client(), sql: r.sql}); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *apiKeyRepository) Create(ctx context.Context, key *service.APIKey) error {
	if len(key.RouteGroupIDs) > 0 {
		ids := key.CandidateGroupIDs()
		if len(ids) != len(key.RouteGroupIDs) {
			return service.ErrGroupNotAllowed
		}
		if key.GroupID == nil || *key.GroupID != ids[0] {
			return service.ErrGroupNotAllowed
		}
	}
	if len(key.RouteGroupIDs) <= 1 {
		return r.create(ctx, key)
	}
	return r.routeTransaction(ctx, func(repo *apiKeyRepository) error {
		if err := repo.create(ctx, key); err != nil {
			return err
		}
		return repo.replaceGroupRoutes(ctx, key.ID, key.RouteGroupIDs)
	})
}
func (r *apiKeyRepository) Update(ctx context.Context, key *service.APIKey, fields service.APIKeyUpdateFields) error {
	if !fields.GroupID && !fields.RouteGroupIDs {
		return r.update(ctx, key, fields)
	}
	return r.routeTransaction(ctx, func(repo *apiKeyRepository) error {
		if err := repo.update(ctx, key, fields); err != nil {
			return err
		}
		ids := key.RouteGroupIDs
		if !fields.RouteGroupIDs {
			ids = nil
			key.RouteGroupIDs = nil
		} // legacy single-group update resets routing
		return repo.replaceGroupRoutes(ctx, key.ID, ids)
	})
}
func (r *apiKeyRepository) replaceGroupRoutes(ctx context.Context, id int64, ids []int64) error {
	if _, err := r.client.APIKeyGroupRoute.Delete().Where(apikeygrouproute.APIKeyIDEQ(id)).Exec(ctx); err != nil {
		return err
	}
	if len(ids) <= 1 {
		return nil
	}
	for i, gid := range ids {
		if _, err := r.client.APIKeyGroupRoute.Create().SetAPIKeyID(id).SetGroupID(gid).SetPosition(i + 1).Save(ctx); err != nil {
			return err
		}
	}
	return nil
}
func (r *apiKeyRepository) attachGroupRoutes(ctx context.Context, keys []service.APIKey) error {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]int64, len(keys))
	positions := make(map[int64]int, len(keys))
	for i := range keys {
		ids[i] = keys[i].ID
		positions[ids[i]] = i
	}
	rows, err := r.client.APIKeyGroupRoute.Query().Where(apikeygrouproute.APIKeyIDIn(ids...)).Order(dbent.Asc(apikeygrouproute.FieldPosition)).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		i := positions[row.APIKeyID]
		keys[i].RouteGroupIDs = append(keys[i].RouteGroupIDs, row.GroupID)
	}
	return nil
}
func (r *apiKeyRepository) withGroupRoutes(ctx context.Context, key *service.APIKey) (*service.APIKey, error) {
	keys := []service.APIKey{*key}
	if err := r.attachGroupRoutes(ctx, keys); err != nil {
		return nil, err
	}
	key.RouteGroupIDs = keys[0].RouteGroupIDs
	return key, nil
}

// Include fallback membership in administration, filtering and cache invalidation.
func apiKeyInGroup(groupID int64) func(*sql.Selector) {
	return func(s *sql.Selector) {
		routes := sql.Table(apikeygrouproute.Table)
		sub := sql.Select(routes.C(apikeygrouproute.FieldAPIKeyID)).From(routes).Where(sql.EQ(routes.C(apikeygrouproute.FieldGroupID), groupID))
		s.Where(sql.Or(sql.EQ(s.C(apikey.FieldGroupID), groupID), sql.In(s.C(apikey.FieldID), sub)))
	}
}
