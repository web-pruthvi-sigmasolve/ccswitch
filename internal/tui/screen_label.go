package tui

import (
	"errors"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ccswitch/ccswitch/internal/identity"
	"github.com/ccswitch/ccswitch/internal/store"
)

// labelScreen asks for a friendly name for one account.
type labelScreen struct {
	sh    *shared
	acc   *store.Account
	next  func() screen
	input textinput.Model
	fp    identity.Fingerprint
	err   string
}

func newLabelScreen(sh *shared, acc *store.Account, next func() screen) *labelScreen {
	ti := textinput.New()
	ti.Prompt = "name: "
	ti.CharLimit = 40
	ti.Width = 30
	ti.Cursor.SetMode(cursor.CursorStatic)
	ti.SetValue(sh.d.Config.Labels[acc.UUID])
	ti.CursorEnd()
	ti.Focus()
	return &labelScreen{sh: sh, acc: acc, next: next, input: ti, fp: identity.FingerprintOf(acc)}
}

func (s *labelScreen) Init() tea.Cmd { return nil }

func (s *labelScreen) capturing() bool { return true }

func (s *labelScreen) helpKeys() helpKeys { return labelKeys() }

func (s *labelScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc":
			return s, goTo(s.next())
		case "enter":
			if err := s.save(); err != nil {
				s.err = err.Error()
				return s, nil
			}
			return s, goTo(s.next())
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return s, cmd
}

func (s *labelScreen) save() error {
	cfg, dir := s.sh.d.Config, s.sh.d.ConfigDir
	if dir == "" {
		return errors.New("there is no config folder to save the name in")
	}
	cfg.SetLabel(s.acc.UUID, s.input.Value())
	if err := cfg.Save(dir); err != nil {
		return err
	}
	return nil
}

func (s *labelScreen) View() string {
	sh, st := s.sh, s.sh.st
	var b strings.Builder
	b.WriteString(st.title.Render("Name this account") + "\n")
	b.WriteString(st.ruleLine(sh.width) + "\n\n")
	b.WriteString(sh.display(s.acc) + "\n")
	var hints []string
	if len(s.fp.TopProjects) > 0 {
		hints = append(hints, "projects: "+strings.Join(s.fp.TopProjects, ", "))
	}
	if len(s.fp.RecentTitles) > 0 {
		hints = append(hints, "recent: \""+strings.Join(s.fp.RecentTitles, "\", \"")+"\"")
	}
	hints = append(hints, pluralize(s.fp.Chats, "chat")+", last active "+relTime(sh.now(), s.fp.LastActive))
	for _, h := range hints {
		b.WriteString(st.dim.Render("  "+st.fit(h, sh.width-4)) + "\n")
	}
	b.WriteString("\nA short name helps you recognise it next time. Leave it empty to remove a name.\n\n")
	b.WriteString(s.input.View() + "\n")
	if s.err != "" {
		b.WriteString("\n" + st.bad.Render(st.fit("Could not save: "+s.err, sh.width-2)) + "\n")
	}
	return b.String()
}
