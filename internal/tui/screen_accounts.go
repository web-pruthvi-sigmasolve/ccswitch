package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/identity"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
)

// accountsScreen lets the user choose the source account.
type accountsScreen struct {
	sh     *shared
	cursor int
	notice string
	fps    map[string]identity.Fingerprint
}

func newAccountsScreen(sh *shared, cursor int) *accountsScreen {
	s := &accountsScreen{sh: sh, cursor: cursor, fps: map[string]identity.Fingerprint{}}
	for _, a := range sh.store.Accounts {
		s.fps[a.UUID] = identity.FingerprintOf(a)
	}
	return s
}

func (s *accountsScreen) Init() tea.Cmd { return nil }

func (s *accountsScreen) helpKeys() helpKeys { return accountsKeys() }

func (s *accountsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	accs := s.sh.store.Accounts
	if len(accs) < 2 {
		return s, tea.Quit
	}
	s.notice = ""
	switch k.String() {
	case "up", "k":
		s.cursor = max(s.cursor-1, 0)
	case "down", "j":
		s.cursor = min(s.cursor+1, len(accs)-1)
	case "q":
		return s, tea.Quit
	case "l":
		a := accs[s.cursor]
		cur := s.cursor
		return s, goTo(newLabelScreen(s.sh, a, func() screen { return newAccountsScreen(s.sh, cur) }))
	case "enter":
		a := accs[s.cursor]
		if a.TotalEntries() == 0 || primaryBucket(a) == nil {
			s.notice = "This account has no chats, so there is nothing to copy from it."
			return s, nil
		}
		sh := s.sh
		if sh.sourceAcc != a {
			sh.resetFlow()
		}
		sh.sourceAcc, sh.source = a, primaryBucket(a)
		next := func() screen { return newDirectionScreen(sh) }
		if sh.res.Label(a.UUID).Source != "user" && !sh.prompted[a.UUID] {
			sh.prompted[a.UUID] = true
			return s, goTo(newLabelScreen(sh, a, next))
		}
		return s, goTo(next())
	}
	return s, nil
}

func (s *accountsScreen) View() string {
	sh, st := s.sh, s.sh.st
	var b strings.Builder
	b.WriteString(st.title.Render("Accounts") + st.dim.Render("  "+st.g.sep+"  "+st.text(sh.d.Version)) + "\n")
	b.WriteString(st.ruleLine(sh.width) + "\n")
	accs := sh.store.Accounts
	if len(accs) < 2 {
		b.WriteString("\nccswitch needs two signed-in Claude Desktop accounts that have used Claude Code.\n")
		b.WriteString(st.text("I found "+pluralize(len(accs), "account")+" in "+sh.store.UserData) + ".\n\n")
		b.WriteString("Sign in to the second account in Claude Desktop, open Claude Code once, then run this again.\n")
		b.WriteString(st.dim.Render("Run `ccswitch doctor` to see where ccswitch looked.") + "\n\n")
		b.WriteString(st.dim.Render("Press any key to quit."))
		return b.String()
	}
	b.WriteString(st.dim.Render("Choose the account to copy chats FROM.") + "\n\n")
	per := 3
	visible := max((sh.avail-5)/per, 1)
	start := windowStart(s.cursor, len(accs), visible)
	for i := start; i < min(start+visible, len(accs)); i++ {
		a := accs[i]
		b.WriteString(sh.accountRow(a, s.fps[a.UUID], i == s.cursor, a.TotalEntries() == 0))
		b.WriteString("\n")
	}
	if s.notice != "" {
		b.WriteString(st.warn.Render(st.text(s.notice)) + "\n")
	}
	return b.String()
}

// windowStart returns the first index of a window of size n that keeps cursor visible.
func windowStart(cursor, total, n int) int {
	if total <= n {
		return 0
	}
	start := cursor - n/2
	return min(max(start, 0), total-n)
}

// accountRow renders an account as two lines plus a blank one: name, chat
// count and last activity, then a dim fingerprint of its projects and titles.
func (sh *shared) accountRow(a *store.Account, fp identity.Fingerprint, cursor, disabled bool) string {
	st := sh.st
	right := pluralize(fp.Chats, "chat") + " " + st.g.sep + " " + relTime(sh.now(), fp.LastActive)
	mark := "  "
	if cursor {
		mark = st.g.cursor + " "
	}
	nameW := max(sh.width-len(mark)-lipgloss.Width(right)-3, 10)
	name := st.fit(sh.display(a), nameW)
	nameStyled := name
	switch {
	case disabled:
		nameStyled = st.dim.Render(name)
	case cursor:
		nameStyled = st.cursorRow.Render(name)
	}
	line1 := mark + padTo(nameStyled, nameW) + "  " + st.dim.Render(right)

	var sub []string
	if disabled {
		sub = append(sub, "no chats - cannot be a source")
	} else {
		if len(fp.TopProjects) > 0 {
			sub = append(sub, strings.Join(fp.TopProjects, ", "))
		}
		for i, t := range fp.RecentTitles {
			if i == 2 {
				break
			}
			sub = append(sub, "\""+t+"\"")
		}
	}
	line2 := "    " + st.dim.Render(st.fit(strings.Join(sub, "  "+st.g.sep+"  "), sh.width-6))
	return line1 + "\n" + line2 + "\n"
}
