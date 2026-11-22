package service

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

func (s *ModelPricingSyncService) syncPublished(ctx context.Context) (applied int, err error) {
	defer func() { s.recordRun(err, applied) }()
	start, err := s.PublishedSettings(ctx)
	if err != nil {
		return 0, err
	}
	c := s.cfg.Collector
	if c == nil {
		return 0, fmt.Errorf("no price collector configured")
	}
	collection, fetchErr := c(ctx, s.fetcher, s.cfg.RegistryURL, start)
	if fetchErr != nil {
		return 0, fetchErr
	} // offline: preserve the original verification time
	_, err = s.publish(ctx, func(h *priceSyncRevision) (bool, error) {
		if h.Revision != start.Revision {
			return false, ErrPriceSyncConflict
		} // never replay a late response
		now := time.Now().UTC()
		if len(collection.SourceErrors) == 0 {
			collection.SourceErrors = nil
		}
		changed := !priceSyncEqual(collection.SourceErrors, h.SourceErrors)
		h.SourceErrors = collection.SourceErrors
		legacyChanged, err := s.reconcileLegacyPrices(ctx, h, &collection)
		if err != nil {
			return false, err
		}
		changed = changed || legacyChanged
		keys := map[string]bool{}
		for k := range h.Models {
			keys[k] = true
		}
		for k := range collection.Quotes {
			keys[k] = true
		}
		for k := range collection.Problems {
			keys[k] = true
		}
		for k := range collection.NativeModels {
			keys[k] = true
		}
		for _, k := range sortedStrings(keys) {
			m := h.Models[k]
			if m == nil {
				m = &priceSyncModel{Mode: "auto"}
				h.Models[k] = m
				changed = true
			}
			if collection.NativeModels[k] && !m.NativeCNY {
				m.NativeCNY = true
				changed = true
			}
			q, ok := collection.Quotes[k]
			issue := collection.Problems[k]
			if !ok && issue == "" {
				issue = "本轮缺少可信同型号报价"
			}
			// An external file row has no V2 ownership. Keep its original
			// ownership/pending diagnosis instead of replacing it with a
			// transient per-run source message.
			if m.External && m.Issue != "" && issue != "" {
				issue = m.Issue
			}
			if ok && issue == "" {
				if prev := m.Auto; prev != nil && prev.Evidence == q.Evidence {
					candidate := q
					candidate.VerifiedAt = prev.VerifiedAt
					if !priceSyncEqual(candidate, prev) {
						issue = "同一来源证据对应不同报价"
					} else if q.VerifiedAt.After(prev.VerifiedAt) && q.VerifiedAt.Sub(prev.VerifiedAt) < priceSyncMaxAge/2 {
						// Do not append a whole revision every tick for the same evidence.
						q.VerifiedAt = prev.VerifiedAt
					}
				}
				if (m.NativeCNY && q.Currency != "CNY") || (!h.Settings.Enabled && q.Currency == "CNY") {
					issue = "人民币策略与报价不符"
				} else if e := priceSyncValid(k, &q, m.Auto, now); e != nil {
					issue = e.Error()
				} else if m.Auto != nil && (q.VerifiedAt.Before(m.Auto.VerifiedAt) || (q.VerifiedAt.Equal(m.Auto.VerifiedAt) && !priceSyncEqual(q, m.Auto))) {
					issue = "旧报价或同一核验时间的冲突报价"
				}
			}
			if issue == "" && m.Mode == "manual" && !m.External && m.Effective != nil && m.Effective.BillingMode == q.BillingMode {
				if e := priceSyncValid(k, &q, m.Effective, now); e != nil {
					issue = e.Error()
				}
			}
			if issue != "" {
				if m.LegacyQuarantine != nil {
					issue = m.Issue
				}
				if m.Issue != issue {
					m.Issue = issue
					changed = true
				}
				continue
			}
			if !m.External && m.Issue != "" {
				m.Issue = ""
				changed = true
			}
			keepRestoreProjection := m.Mode == "auto" && m.PreserveRestoreProjection && samePriceSyncTariff(m.Auto, &q)
			if !priceSyncEqual(m.Auto, q) {
				m.Auto = &q
				changed = true
			}
			if q.Currency == "CNY" && !m.NativeCNY {
				m.NativeCNY = true
				changed = true
			}
			if m.External {
				continue
			}
			if p, manual := h.Settings.ManualOverrides[k]; manual {
				if !p.AutoRestore {
					continue
				}
				if e := restorePriceSync(k, m, now, h.Entries[k], p); e != nil {
					continue
				}
				delete(h.Settings.ManualOverrides, k)
				changed = true
			} else {
				m.Mode = "auto"
				if keepRestoreProjection {
					effective := *m.Auto
					effective.Rates, effective.Tiers = m.Effective.Rates, m.Effective.Tiers
					m.Effective = &effective
				} else {
					m.Effective = m.Auto
					m.PreserveRestoreProjection = false
				}
			}
			// A verified quote replaces the whole row, including a prior legacy
			// quarantine marker. Clear the policy state in the same revision.
			if m.LegacyAuto || m.LegacyQuarantine != nil {
				m.LegacyAuto = false
				m.LegacyQuarantine = nil
				changed = true
			}
			if m.preserveLegacyRestoreEntry(h.Entries[k]) {
				continue
			}
			row, e := priceSyncRender(h.Entries[k], m.Effective, nil, m.NativeCNY)
			if e != nil {
				return false, e
			}
			if !samePriceSyncRow(h.Entries[k], row) {
				applied++
				h.Entries[k] = row
				changed = true
			}
		}
		return changed, nil
	})
	if err != nil {
		return 0, err
	}
	return applied, nil
}

func samePriceSyncRow(a, b json.RawMessage) bool {
	if priceSyncEqual(a, b) {
		return true
	}
	var x, y map[string]any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}
