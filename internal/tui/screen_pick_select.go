package tui

import (
	"github.com/ccswitch/ccswitch/internal/plan"
	"github.com/ccswitch/ccswitch/internal/store"
)

func (s *pickScreen) matchedEntries() []*store.Entry {
	var out []*store.Entry
	for _, r := range s.rows {
		if r.e == nil {
			out = append(out, r.matched...)
		}
	}
	return out
}

func (s *pickScreen) setAll(es []*store.Entry, on bool) {
	for _, e := range es {
		if on {
			s.sh.selected[e.SessionID] = true
		} else {
			delete(s.sh.selected, e.SessionID)
		}
	}
}

func (s *pickScreen) toggleGroup(es []*store.Entry) {
	all := true
	for _, e := range es {
		all = all && s.sh.selected[e.SessionID]
	}
	s.setAll(es, !all)
}

func (s *pickScreen) jumpGroup(dir int) {
	for i := s.cursor + dir; i >= 0 && i < len(s.rows); i += dir {
		if s.rows[i].e == nil {
			s.move(i)
			return
		}
	}
}

func (s *pickScreen) cycleOverride(r pickRow) {
	sh := s.sh
	if op, ok := s.preview[r.e.SessionID]; !ok || op.Class != plan.DestNewer {
		s.notice = "Only chats where the destination is newer have a choice."
		return
	}
	src := plan.Merge
	if sh.opts.Mode == plan.ModeOverwrite {
		src = plan.Overwrite
	}
	cur, has := sh.overrides[r.e.SessionID]
	switch {
	case !has:
		sh.overrides[r.e.SessionID] = plan.Skip
	case cur == plan.Skip:
		sh.overrides[r.e.SessionID] = src
	default:
		delete(sh.overrides, r.e.SessionID)
	}
}
