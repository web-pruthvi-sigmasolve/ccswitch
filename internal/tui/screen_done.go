package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ccswitch/ccswitch/internal/identity"
	"github.com/ccswitch/ccswitch/internal/store"
)

// doneScreen reports what happened.
type doneScreen struct {
	sh  *shared
	err string // restart error
}

func newDoneScreen(sh *shared) *doneScreen { return &doneScreen{sh: sh} }

func (s *doneScreen) Init() tea.Cmd { return nil }

func (s *doneScreen) helpKeys() helpKeys { return doneKeys() }

func (s *doneScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch k.String() {
	case "enter", "q":
		return s, tea.Quit
	case "r":
		sh := s.sh
		st, err := store.Discover(store.Options{
			UserDataOverride:       sh.store.UserData,
			TranscriptRootOverride: sh.store.TranscriptRoot,
		})
		if err != nil || st == nil {
			s.err = fmt.Sprint("could not read the session store again: ", err)
			return s, nil
		}
		sh.store = st
		sh.res = identity.New(st, sh.d.Config)
		sh.resetFlow()
		return s, goTo(newAccountsScreen(sh, 0))
	}
	return s, nil
}

func (s *doneScreen) View() string {
	sh, st := s.sh, s.sh.st
	r := sh.result
	var b strings.Builder
	b.WriteString(st.title.Render("Done") + "\n" + st.ruleLine(sh.width) + "\n\n")
	if r == nil {
		return b.String() + "Nothing was applied.\n"
	}
	head := st.ok.Render(st.g.add)
	if r.Failed > 0 || sh.applyErr != nil {
		head = st.warn.Render(st.g.conflict)
	}
	fmt.Fprintf(&b, "%s applied %d  %s  failed %d  %s  skipped %d\n\n", head, r.Applied, st.g.sep, r.Failed, st.g.sep, r.Skipped)
	if sh.applyErr != nil {
		b.WriteString(st.bad.Render(st.fit("Problem: "+sh.applyErr.Error(), sh.width-2)) + "\n\n")
	}
	switch {
	case r.BackupSet != nil:
		b.WriteString(st.fit(fmt.Sprintf("Backup %s (%s) saved in", r.BackupSet.ID, pluralize(len(r.BackupSet.Files), "file")), sh.width) + "\n")
		for _, l := range strings.Split(ansi.Hardwrap(st.text(r.BackupSet.Dir), max(sh.width-2, 10), true), "\n") {
			b.WriteString("  " + st.accent.Render(l) + "\n")
		}
		b.WriteString(st.dim.Render("Undo with: ccswitch undo") + "\n\n")
	case r.Applied > 0:
		b.WriteString(st.dim.Render("Nothing was overwritten, so no backup was needed.") + "\n\n")
	}
	if r.Applied > 0 {
		b.WriteString("Switch account in Claude Desktop (or restart it) to refresh the sidebar.\n\n")
	}
	shown := 0
	for _, ev := range r.Events {
		if ev.Status != "failed" || ev.Err == nil {
			continue
		}
		if shown == 0 {
			b.WriteString(st.bad.Render("Failed") + "\n")
		}
		if shown++; shown > max(sh.avail-14, 3) {
			b.WriteString(st.dim.Render("  ...and more") + "\n")
			break
		}
		title := ev.Op.Source.Title
		if title == "" {
			title = ev.Op.Source.SessionID
		}
		msg := strings.SplitN(ev.Err.Error(), "\n", 2)[0]
		b.WriteString("  " + st.bad.Render(st.g.orphan) + " " + st.fit(title+": "+msg, sh.width-6) + "\n")
	}
	if sh.quitRequested {
		b.WriteString(st.warn.Render(st.fit("You pressed ctrl+c, so the apply finished first. Press enter to quit.", sh.width)) + "\n")
	}
	if s.err != "" {
		b.WriteString(st.bad.Render(st.fit(s.err, sh.width)) + "\n")
	}
	return b.String()
}
