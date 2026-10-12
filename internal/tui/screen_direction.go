package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ccswitch/ccswitch/internal/identity"
	"github.com/ccswitch/ccswitch/internal/plan"
	"github.com/ccswitch/ccswitch/internal/store"
)

// directionScreen lets the user choose the destination account.
type directionScreen struct {
	sh     *shared
	cands  []*store.Account
	cursor int
	fps    map[string]identity.Fingerprint
}

func newDirectionScreen(sh *shared) *directionScreen {
	s := &directionScreen{sh: sh, cands: sortedAccounts(sh.store, sh.sourceAcc), fps: map[string]identity.Fingerprint{}}
	for _, a := range s.cands {
		s.fps[a.UUID] = identity.FingerprintOf(a)
	}
	return s
}

func (s *directionScreen) Init() tea.Cmd { return nil }

func (s *directionScreen) helpKeys() helpKeys { return directionKeys() }

func (s *directionScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	sh := s.sh
	switch k.String() {
	case "up", "k":
		s.cursor = max(s.cursor-1, 0)
	case "down", "j":
		s.cursor = min(s.cursor+1, len(s.cands)-1)
	case "q":
		return s, tea.Quit
	case "esc":
		return s, goTo(newAccountsScreen(sh, s.sourceIndex()))
	case "enter":
		if len(s.cands) == 0 {
			return s, nil
		}
		acc := s.cands[s.cursor]
		dst := destBucketFor(acc, sh.source.OrgUUID)
		if sh.dest == nil || sh.dest.Key() != dst.Key() {
			sh.selected = map[string]bool{}
			sh.overrides = map[string]plan.Action{}
			sh.optsGen++
		}
		sh.destAcc, sh.dest = acc, dst
		return s, goTo(newPickScreen(sh))
	}
	return s, nil
}

func (s *directionScreen) sourceIndex() int {
	for i, a := range s.sh.store.Accounts {
		if a == s.sh.sourceAcc {
			return i
		}
	}
	return 0
}

// orgWarning is the cross-org note for sending chats to acc, or "".
func (s *directionScreen) orgWarning(acc *store.Account) string {
	dst := destBucketFor(acc, s.sh.source.OrgUUID)
	if dst.OrgUUID == s.sh.source.OrgUUID {
		return ""
	}
	return "Different organization: connectors and settings tied to the organization may not match."
}

func (s *directionScreen) View() string {
	sh, st := s.sh, s.sh.st
	var b strings.Builder
	b.WriteString(st.title.Render("Direction") + "\n")
	b.WriteString(st.ruleLine(sh.width) + "\n")
	b.WriteString(st.bold.Render("FROM "+st.fit(sh.display(sh.sourceAcc), sh.width/2-6)) +
		"  " + st.g.arrow + "  " + st.bold.Render("TO ?") + "\n\n")
	b.WriteString(st.dim.Render("Choose the account to copy chats TO.") + "\n\n")
	visible := max((sh.avail-8)/3, 1)
	start := windowStart(s.cursor, len(s.cands), visible)
	for i := start; i < min(start+visible, len(s.cands)); i++ {
		a := s.cands[i]
		b.WriteString(sh.accountRow(a, s.fps[a.UUID], i == s.cursor, false) + "\n")
	}
	if len(s.cands) > 0 {
		if w := s.orgWarning(s.cands[s.cursor]); w != "" {
			b.WriteString(st.warn.Render(st.g.conflict+" "+st.fit(w, sh.width-4)) + "\n")
		}
	}
	return b.String()
}
