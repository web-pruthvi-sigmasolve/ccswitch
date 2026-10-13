package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/plan"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
)

// pickGroup is one project folder with its chats, newest first. Groups are
// built once; filtering and collapsing only rebuild the flat row list.
type pickGroup struct {
	key, name string
	entries   []*store.Entry
	newest    int64
}

// pickRow is one line of the flattened list: a group header (e == nil) or a chat.
type pickRow struct {
	g       *pickGroup
	e       *store.Entry
	matched []*store.Entry // header rows: the chats that match the filter
}

// pickScreen is the per-chat selection list.
type pickScreen struct {
	sh        *shared
	groups    []*pickGroup
	hay       map[string]string // lower-case "title\x00project" per session
	collapsed map[string]bool
	rows      []pickRow
	cursor    int
	offset    int
	input     textinput.Model
	filtering bool
	query     string
	preview   map[string]plan.Op
	previewG  int
	previewE  string
	notice    string
}

func newPickScreen(sh *shared) *pickScreen {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.Placeholder = "title or project"
	ti.CharLimit = 80
	ti.Width = 30
	ti.Cursor.SetMode(cursor.CursorStatic)
	s := &pickScreen{sh: sh, hay: map[string]string{}, collapsed: map[string]bool{}, input: ti}
	byKey := map[string]*pickGroup{}
	for _, e := range sh.source.Entries {
		g := byKey[e.ProjectDir()]
		if g == nil {
			g = &pickGroup{key: e.ProjectDir(), name: sh.st.text(e.Project())}
			byKey[g.key] = g
			s.groups = append(s.groups, g)
		}
		g.entries = append(g.entries, e)
		g.newest = max(g.newest, e.Activity())
		s.hay[e.SessionID] = strings.ToLower(e.Title + "\x00" + e.Project())
	}
	for _, g := range s.groups {
		sort.SliceStable(g.entries, func(i, j int) bool { return g.entries[i].Activity() > g.entries[j].Activity() })
	}
	sort.SliceStable(s.groups, func(i, j int) bool { return s.groups[i].newest > s.groups[j].newest })
	s.rebuildPreview()
	s.rebuildRows()
	return s
}

// Init refreshes the status glyphs when the options changed on the Review screen.
func (s *pickScreen) Init() tea.Cmd {
	if s.previewG != s.sh.optsGen {
		s.rebuildPreview()
	}
	return nil
}

func (s *pickScreen) capturing() bool { return s.filtering }

func (s *pickScreen) helpKeys() helpKeys { return pickKeys() }

// rebuildPreview plans every chat of the source against the destination once,
// so each row can show how it relates to the destination.
func (s *pickScreen) rebuildPreview() {
	sh := s.sh
	opts := sh.opts
	opts.Overrides = nil
	s.preview, s.previewE, s.previewG = map[string]plan.Op{}, "", sh.optsGen
	p, err := plan.Build(sh.source, sh.dest, sh.source.Entries, opts)
	if err != nil {
		s.previewE = err.Error()
		return
	}
	for _, op := range p.Ops {
		s.preview[op.Source.SessionID] = op
	}
}

func (s *pickScreen) rebuildRows() {
	var prevG *pickGroup
	var prevE *store.Entry
	if s.cursor < len(s.rows) {
		prevG, prevE = s.rows[s.cursor].g, s.rows[s.cursor].e
	}
	s.rows = s.rows[:0]
	for _, g := range s.groups {
		var m []*store.Entry
		for _, e := range g.entries {
			if s.query == "" || strings.Contains(s.hay[e.SessionID], s.query) {
				m = append(m, e)
			}
		}
		if len(m) == 0 {
			continue
		}
		s.rows = append(s.rows, pickRow{g: g, matched: m})
		if !s.collapsed[g.key] {
			for _, e := range m {
				s.rows = append(s.rows, pickRow{g: g, e: e})
			}
		}
	}
	for i, r := range s.rows {
		if r.g == prevG && r.e == prevE {
			s.cursor = i
		}
	}
	s.cursor = min(s.cursor, max(len(s.rows)-1, 0))
	s.ensureVisible()
}

func (s *pickScreen) pageSize() int { return max(s.sh.avail-6, 3) }

func (s *pickScreen) ensureVisible() {
	n := s.pageSize()
	if s.cursor < s.offset {
		s.offset = s.cursor
	}
	if s.cursor >= s.offset+n {
		s.offset = s.cursor - n + 1
	}
	s.offset = max(min(s.offset, len(s.rows)-n), 0)
}

func (s *pickScreen) move(to int) {
	s.cursor = max(min(to, len(s.rows)-1), 0)
	s.ensureVisible()
}

func (s *pickScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	s.notice = ""
	key := k.String()
	if s.filtering && key != "up" && key != "down" {
		switch key {
		case "esc":
			s.filtering, s.query = false, ""
			s.input.Blur()
			s.input.SetValue("")
			s.rebuildRows()
		case "enter":
			s.filtering = false
			s.input.Blur()
		default:
			var cmd tea.Cmd
			s.input, cmd = s.input.Update(msg)
			if q := strings.ToLower(strings.TrimSpace(s.input.Value())); q != s.query {
				s.query = q
				s.rebuildRows()
			}
			return s, cmd
		}
		return s, nil
	}
	var cur pickRow
	if s.cursor < len(s.rows) {
		cur = s.rows[s.cursor]
	}
	switch key {
	case "up", "k":
		s.move(s.cursor - 1)
	case "down", "j":
		s.move(s.cursor + 1)
	case "pgup":
		s.move(s.cursor - s.pageSize())
	case "pgdown":
		s.move(s.cursor + s.pageSize())
	case "tab":
		s.jumpGroup(1)
	case "shift+tab":
		s.jumpGroup(-1)
	case " ":
		if cur.g == nil {
			break
		}
		if cur.e != nil {
			s.toggleGroup([]*store.Entry{cur.e})
		} else {
			s.toggleGroup(cur.matched)
		}
	case "a":
		s.setAll(s.matchedEntries(), true)
	case "n":
		s.setAll(s.matchedEntries(), false)
	case "p":
		if cur.g != nil {
			s.collapsed[cur.g.key] = !s.collapsed[cur.g.key]
			for i, r := range s.rows {
				if r.g == cur.g && r.e == nil {
					s.cursor = i
				}
			}
			s.rebuildRows()
		}
	case "c":
		if cur.e != nil {
			s.cycleOverride(cur)
		}
	case "/":
		s.filtering = true
		return s, s.input.Focus()
	case "q":
		return s, tea.Quit
	case "esc":
		if s.query != "" {
			s.query = ""
			s.input.SetValue("")
			s.rebuildRows()
			return s, nil
		}
		d := newDirectionScreen(s.sh)
		for i, a := range d.cands {
			if a == s.sh.destAcc {
				d.cursor = i
			}
		}
		return s, goTo(d)
	case "enter":
		switch {
		case len(s.sh.selected) == 0:
			s.notice = "Select at least one chat first (space)."
		default:
			return s, goTo(newReviewScreen(s.sh, s))
		}
	}
	return s, nil
}
