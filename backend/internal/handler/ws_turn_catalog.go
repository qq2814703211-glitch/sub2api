package handler

import (
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type wsCatalogSlot struct {
	turn    int
	catalog *service.RequestCatalog
}

// A new turn may start before the preceding AfterTurn callback. Keep both
// handles, not a connection-long history. Workers take their own reference.
type wsTurnCatalogs struct {
	mu                sync.Mutex
	previous, current wsCatalogSlot
}

func (s *wsTurnCatalogs) store(turn int, p *service.RequestCatalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn > s.current.turn {
		s.previous, s.current = s.current, wsCatalogSlot{turn, p}
	}
}

func (s *wsTurnCatalogs) get(turn int) *service.RequestCatalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn == s.current.turn {
		return s.current.catalog
	}
	if turn == s.previous.turn {
		return s.previous.catalog
	}
	return nil
}

func (s *wsTurnCatalogs) bindGroup(turn int, g *service.Group) *service.RequestCatalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	if turn == s.current.turn {
		s.current.catalog = s.current.catalog.WithPricingGroup(g)
		return s.current.catalog
	}
	if turn == s.previous.turn {
		return s.previous.catalog
	}
	return nil
}

func (s *wsTurnCatalogs) latest() *service.RequestCatalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current.catalog
}

func (s *wsTurnCatalogs) restart(p *service.RequestCatalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previous, s.current = wsCatalogSlot{}, wsCatalogSlot{1, p}
}
