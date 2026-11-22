package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"
)

// Hash the effective source row once during loading, never during lookup. The
// row digest covers the full tariff and provenance, not just the base rate.
type catalogRowIdentity struct {
	SKU    string `json:"sku,omitempty"`
	Digest string `json:"digest,omitempty"`
	Origin string `json:"origin,omitempty"`
}

func identifyCatalogRow(sku string, raw, source json.RawMessage) catalogRowIdentity {
	digest := sha256.Sum256(raw)
	id := catalogRowIdentity{SKU: sku, Digest: hex.EncodeToString(digest[:]), Origin: "catalog"}
	var src struct{ Provider, Currency string }
	if len(source) > 0 && json.Unmarshal(source, &src) == nil && src.Provider != "" {
		id.Origin = "auto"
		if src.Provider == "operator" && src.Currency == "SITE" {
			id.Origin = "manual"
		}
	}
	return id
}

type priceBasis struct {
	Model         string             `json:"billing_model,omitempty"`
	Scope         string             `json:"scope,omitempty"`
	Row           catalogRowIdentity `json:"base_row"`
	CardID        int64              `json:"card_id,omitempty"`
	CardUpdatedAt *time.Time         `json:"card_updated_at,omitempty"`
}

func (b *priceBasis) set(model, scope string, card *ChannelModelPricing) {
	b.Model, b.Scope = model, scope
	if scope == "" || scope == PricingSourceLiteLLM {
		b.Scope = "catalog"
		if b.Row.SKU == "" {
			b.Scope = "builtin"
		}
	}
	if card != nil {
		b.CardID = card.ID
		if !card.UpdatedAt.IsZero() {
			at := card.UpdatedAt
			b.CardUpdatedAt = &at
		}
	}
}

type RequestPriceTrace struct {
	PricingAt      time.Time  `json:"pricing_at"`
	Revision       string     `json:"revision,omitempty"`
	BaseHash       string     `json:"base_hash,omitempty"`
	LayersHash     string     `json:"layers_hash,omitempty"`
	GroupUpdatedAt *time.Time `json:"group_updated_at,omitempty"`
	Basis          priceBasis `json:"basis"`
}

func (p *RequestCatalog) trace(cost *CostBreakdown, model string, group *Group) *RequestPriceTrace {
	if p == nil || cost == nil {
		return nil
	}
	t := &RequestPriceTrace{PricingAt: p.at, Revision: p.revision, BaseHash: p.baseHash, LayersHash: p.layersHash, Basis: cost.priceBasis}
	if t.Basis.Model == "" {
		t.Basis.Model = model
	}
	if t.Basis.Scope == "" && t.Basis.Row.SKU != "" {
		t.Basis.Scope = "catalog"
	}
	if group != nil && !group.UpdatedAt.IsZero() {
		at := group.UpdatedAt
		t.GroupUpdatedAt = &at
	}
	return t
}

func logRequestPriceTrace(ctx context.Context, u *UsageLog) {
	if u != nil && u.PriceTrace != nil {
		var groupID any
		if u.GroupID != nil {
			groupID = *u.GroupID
		}
		slog.InfoContext(ctx, "usage_pricing_basis", "request_id", u.RequestID, "api_key_id", u.APIKeyID, "group_id", groupID, "account_id", u.AccountID, "actual_cost", u.ActualCost, "price", u.PriceTrace)
	}
}
