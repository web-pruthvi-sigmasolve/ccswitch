package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ccswitch/ccswitch/internal/config"
	"github.com/ccswitch/ccswitch/internal/plan"
	"github.com/ccswitch/ccswitch/internal/store"
)

// desktopMsg reports whether Claude Desktop is running.
type desktopMsg struct{ running bool }

// reviewScreen shows the dry run: exactly what apply will do.
type reviewScreen struct {
	sh          *shared
	back        screen
	vp          viewport.Model
	showSkipped bool
	desktop     bool
	err         string
}

func newReviewScreen(sh *shared, back screen) *reviewScreen {
	s := &reviewScreen{sh: sh, back: back, vp: viewport.New(sh.width, 5)}
	s.rebuild()
	return s
}

// Init checks once whether Claude Desktop is running.
func (s *reviewScreen) Init() tea.Cmd {
	return func() tea.Msg { return desktopMsg{running: store.ClaudeDesktopRunning()} }
}

func (s *reviewScreen) helpKeys() helpKeys { return reviewKeys() }

func (s *reviewScreen) rebuild() {
	p, err := s.sh.buildPlan()
	s.err = ""
	if err != nil {
		s.err = err.Error()
	}
	s.sh.plan = p
	s.vp.Width = s.sh.width
	s.vp.SetContent(s.content())
	s.vp.GotoTop()
}

func (s *reviewScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	sh := s.sh
	switch msg := msg.(type) {
	case desktopMsg:
		s.desktop = msg.running
	case tea.WindowSizeMsg:
		s.rebuild()
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return s, tea.Quit
		case "esc":
			return s, goTo(s.back)
		case "m":
			sh.opts.Mode = (sh.opts.Mode + 1) % 3
			sh.optsGen++
			s.rebuild()
		case "c":
			sh.opts.OnConflict = (sh.opts.OnConflict + 1) % 3
			sh.optsGen++
			s.rebuild()
		case "o":
			sh.opts.IncludeOrphans = !sh.opts.IncludeOrphans
			sh.optsGen++
			s.rebuild()
		case "s":
			s.showSkipped = !s.showSkipped
			s.vp.SetContent(s.content())
		case "enter":
			if sh.plan != nil && len(sh.plan.Changes()) > 0 {
				return s, goTo(newApplyScreen(sh, s))
			}
		default:
			s.vp.Height = s.viewHeight()
			var cmd tea.Cmd
			s.vp, cmd = s.vp.Update(msg)
			return s, cmd
		}
	}
	return s, nil
}

func (s *reviewScreen) viewHeight() int {
	return max(s.sh.avail-lipgloss.Height(s.header())-1, 3)
}

// reason explains why enter does nothing, or "" when applying is possible.
func (s *reviewScreen) disabledReason() string {
	p := s.sh.plan
	switch {
	case p == nil:
		return "the plan could not be built"
	case len(p.Ops) == 0:
		return "no chats are selected"
	case len(p.Changes()) == 0:
		return "every selected chat is already identical, kept or skipped; change the mode (m), conflict policy (c) or orphans (o)"
	}
	return ""
}

func (s *reviewScreen) header() string {
	sh, st := s.sh, s.sh.st
	w := sh.width
	var b strings.Builder
	b.WriteString(st.title.Render("Review") + st.dim.Render("  "+st.g.sep+"  dry run, nothing is written yet") + "\n" + st.ruleLine(w) + "\n")
	b.WriteString(st.bold.Render("FROM "+st.fit(sh.display(sh.sourceAcc), w/2-6)) + "  " + st.g.arrow + "  " +
		st.bold.Render("TO "+st.fit(sh.display(sh.destAcc), w/2-6)) + "\n")
	if s.err != "" {
		b.WriteString(st.bad.Render(st.fit(s.err, w)) + "\n")
		return b.String()
	}
	p := sh.plan
	b.WriteString(st.fit(p.Summary(), w) + "\n")
	opt := sh.opts
	orph := "skipped"
	if opt.IncludeOrphans {
		orph = "included"
	}
	b.WriteString(st.dim.Render(st.fit(fmt.Sprintf("mode: %s  %s  on conflict: %s  %s  orphans: %s",
		opt.Mode, st.g.sep, opt.OnConflict, st.g.sep, orph), w)) + "\n")
	if p.Merges+p.Overwrites > 0 {
		dir := filepath.Join(config.BackupDir(sh.d.ConfigDir), "<timestamp>")
		b.WriteString("Backup " + st.g.arrow + " " + st.fitTail(dir, w-10) + "\n")
	} else {
		b.WriteString(st.dim.Render("No existing file is changed, so no backup is needed.") + "\n")
	}
	if len(p.Ops) > 0 && p.Ops[0].CrossOrg {
		b.WriteString(st.warn.Render(st.wrap(st.g.conflict+" Different organization: connectors and settings tied to the organization may not match.", w)) + "\n")
	}
	if s.desktop {
		b.WriteString(st.warn.Render(st.wrap(st.g.conflict+" Claude Desktop is running. Switch account or restart it afterwards to see the chats; ccswitch never closes it.", w)) + "\n")
	}
	if r := s.disabledReason(); r != "" {
		b.WriteString(st.warn.Render(st.wrap("Nothing to apply: "+r+".", w)) + "\n")
	}
	return b.String()
}

func (s *reviewScreen) opLine(op plan.Op, glyph string, sty lipgloss.Style) string {
	st := s.sh.st
	w := s.sh.width - 6
	proj := st.fit(op.Source.Project(), 24)
	title := op.Source.Title
	if title == "" {
		title = "(untitled)"
	}
	line := " " + sty.Render(glyph) + " " + padTo(st.fit(title, max(w-lipgloss.Width(proj)-2, 8)), max(w-lipgloss.Width(proj)-2, 8)) + "  " + st.dim.Render(proj)
	line += "\n     " + st.dim.Render(st.fit(op.Reason, w))
	if len(op.DiffKeys) > 0 && op.Class == plan.DestNewer {
		line += "\n     " + st.dim.Render(st.fit("differs: "+strings.Join(op.DiffKeys, ", "), w))
	}
	return line
}

func (s *reviewScreen) content() string {
	p, st := s.sh.plan, s.sh.st
	if p == nil {
		return ""
	}
	var out []string
	section := func(title string) { out = append(out, "", st.bold.Render(title)) }
	for _, act := range []plan.Action{plan.Add, plan.Merge, plan.Overwrite} {
		first := true
		for _, op := range p.Ops {
			if op.Action != act {
				continue
			}
			if first {
				section(map[plan.Action]string{plan.Add: "To add", plan.Merge: "To merge", plan.Overwrite: "To overwrite"}[act])
				first = false
			}
			g, sty := st.g.add, st.ok
			if act == plan.Merge {
				g, sty = st.g.merge, st.accent
			} else if act == plan.Overwrite {
				g, sty = st.g.conflict, st.warn
			}
			out = append(out, s.opLine(op, g, sty))
		}
	}
	var other []plan.Op
	first := true
	for _, op := range p.Ops {
		switch {
		case op.Action != plan.Skip:
		case op.Class == plan.DestNewer:
			if first {
				section("Kept as they are (destination is newer)")
				first = false
			}
			out = append(out, s.opLine(op, st.g.conflict, st.warn))
		default:
			other = append(other, op)
		}
	}
	if len(other) > 0 {
		section(fmt.Sprintf("%d skipped", len(other)))
		if !s.showSkipped {
			out[len(out)-1] = st.bold.Render(fmt.Sprintf("%d skipped", len(other))) + st.dim.Render(" (press s to show)")
		}
		for _, op := range other {
			if s.showSkipped {
				g := st.g.same
				if op.Class == plan.Orphan || op.Class == plan.Unreadable {
					g = st.g.orphan
				}
				out = append(out, s.opLine(op, g, st.dim))
			}
		}
	}
	return strings.Join(out, "\n")
}

func (s *reviewScreen) View() string {
	sh := s.sh
	vp := s.vp
	vp.Height = s.viewHeight()
	vp.Width = sh.width
	return s.header() + vp.View()
}
