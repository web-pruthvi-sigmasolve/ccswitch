package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ccswitch/ccswitch/internal/plan"
	"github.com/ccswitch/ccswitch/internal/store"
)

// statusColW is the width of the right-hand status column.
const statusColW = 27

// status describes how a chat relates to the destination.
func (s *pickScreen) status(e *store.Entry) (glyph, label string, sty lipgloss.Style) {
	st := s.sh.st
	op, ok := s.preview[e.SessionID]
	switch {
	case !ok:
		return "", "", st.dim
	case op.Class == plan.Unreadable:
		return st.g.orphan, "unreadable dest", st.bad
	case op.Class == plan.Orphan || !e.TranscriptExists:
		return st.g.orphan, "no transcript", st.bad
	case op.Class == plan.Missing:
		return st.g.add, "new", st.ok
	case op.Class == plan.SourceNewer:
		return st.g.merge, "newer", st.accent
	case op.Class == plan.DestNewer:
		return st.g.conflict, "dest newer", st.warn
	case op.Class == plan.MetaOnly:
		return st.g.same, "already there", st.dim
	}
	return st.g.same, "already there", st.dim
}

func (s *pickScreen) renderRow(i int) string {
	sh, st := s.sh, s.sh.st
	r := s.rows[i]
	mark := "  "
	if i == s.cursor {
		mark = st.g.cursor + " "
	}
	if r.e == nil {
		n := 0
		for _, e := range r.matched {
			if sh.selected[e.SessionID] {
				n++
			}
		}
		box, chev := st.g.checkOff, st.g.expanded
		if n == len(r.matched) {
			box = st.g.checkOn
		} else if n > 0 {
			box = st.g.checkPart
		}
		if s.collapsed[r.g.key] {
			chev = st.g.collapsed
		}
		count := fmt.Sprintf("(%d)", len(r.matched))
		if len(r.matched) != len(r.g.entries) {
			count = fmt.Sprintf("(%d of %d)", len(r.matched), len(r.g.entries))
		}
		name := st.fit(r.g.name, sh.width-len(mark)-lipgloss.Width(box)-lipgloss.Width(count)-6)
		return mark + st.bold.Render(chev+" "+box+" "+name) + " " + st.dim.Render(count)
	}
	box := st.g.checkOff
	if sh.selected[r.e.SessionID] {
		box = st.g.checkOn
	}
	glyph, label, sty := s.status(r.e)
	right := ""
	if label != "" {
		right = glyph + " " + label
		if ov, has := sh.overrides[r.e.SessionID]; has {
			right += map[bool]string{true: " [keep]", false: " [source]"}[ov == plan.Skip]
		}
	}
	title := r.e.Title
	if title == "" {
		title = "(untitled)"
	}
	suffix := ""
	if r.e.IsArchived {
		suffix = " (archived)"
	}
	titleW := max(sh.width-len(mark)-2-lipgloss.Width(box)-1-statusColW-1, 8)
	t := st.fit(title, titleW-len(suffix))
	cell := padTo(t+st.dim.Render(suffix), titleW)
	if i == s.cursor {
		cell = padTo(st.cursorRow.Render(t)+st.dim.Render(suffix), titleW)
	}
	return mark + "  " + box + " " + cell + " " + sty.Render(padTo(st.text(right), statusColW))
}

func (s *pickScreen) View() string {
	sh, st := s.sh, s.sh.st
	var b strings.Builder
	b.WriteString(st.title.Render("Pick chats") + "\n" + st.ruleLine(sh.width) + "\n")
	b.WriteString(st.bold.Render("FROM "+st.fit(sh.display(sh.sourceAcc), sh.width/2-6)) + "  " + st.g.arrow +
		"  " + st.bold.Render("TO "+st.fit(sh.display(sh.destAcc), sh.width/2-6)) + "\n")
	switch {
	case s.filtering:
		b.WriteString(s.input.View() + "\n")
	case s.query != "":
		b.WriteString(st.accent.Render(st.fit("filter: "+s.query, sh.width)) + st.dim.Render("  (esc clears)") + "\n")
	default:
		b.WriteString(st.dim.Render("press / to filter by title or project") + "\n")
	}
	end := min(s.offset+s.pageSize(), len(s.rows))
	for i := s.offset; i < end; i++ {
		b.WriteString(s.renderRow(i) + "\n")
	}
	if len(s.rows) == 0 {
		b.WriteString(st.dim.Render("  no chats match the filter") + "\n")
	}
	foot := fmt.Sprintf("%d selected of %d", len(sh.selected), len(sh.source.Entries))
	if s.previewE != "" {
		foot += "  " + st.bad.Render(st.fit(s.previewE, sh.width/2))
	}
	if s.notice != "" {
		foot += "  " + st.warn.Render(st.fit(s.notice, sh.width-len(foot)-4))
	}
	b.WriteString(st.ruleLine(sh.width) + "\n" + foot)
	return b.String()
}
