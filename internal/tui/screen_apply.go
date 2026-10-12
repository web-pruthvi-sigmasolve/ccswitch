package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ccswitch/ccswitch/internal/apply"
	"github.com/ccswitch/ccswitch/internal/backup"
	"github.com/ccswitch/ccswitch/internal/config"
	"github.com/ccswitch/ccswitch/internal/store"
)

type (
	applyStartedMsg struct{ ch <-chan tea.Msg }
	applyEventMsg   struct{ ev apply.Event }
	applyDoneMsg    struct {
		res *apply.Result
		err error
	}
	applyLockErrMsg struct{ err error }
)

// applyScreen takes the lock, runs apply.Run in the background and streams
// its progress into a scrolling log.
type applyScreen struct {
	sh   *shared
	back screen
	spin spinner.Model
	log  []string
	seen int
	ch   <-chan tea.Msg
	err  string
	// finished is set once the apply goroutine reported back (or failed to start).
	finished bool
	// quitRequested records a ctrl+c received while the apply was running.
	quitRequested bool
}

// quitNotice is shown when ctrl+c arrives during an apply.
const quitNotice = "Finishing the current chat, then quitting. Nothing is left half-written."

// blocksQuit is true while apply.Run may be writing: quitting then would leave
// an uncommitted backup set that undo cannot see.
func (s *applyScreen) blocksQuit() bool { return s.err == "" && !s.finished }

func newApplyScreen(sh *shared, back screen) *applyScreen {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	if sh.st.ascii {
		sp.Spinner = spinner.Line
	}
	sp.Style = sh.st.accent
	return &applyScreen{sh: sh, back: back, spin: sp}
}

func (s *applyScreen) Init() tea.Cmd { return tea.Batch(s.spin.Tick, s.start) }

func (s *applyScreen) helpKeys() helpKeys { return applyKeys(s.err == "") }

// start runs in a command goroutine: lock, then apply in another goroutine.
func (s *applyScreen) start() tea.Msg {
	sh := s.sh
	p, d := sh.plan, sh.d
	var release func()
	if d.Lock != nil {
		r, err := d.Lock()
		if err != nil {
			return applyLockErrMsg{err}
		}
		release = r
	}
	ch := make(chan tea.Msg, 64)
	opts := apply.Options{
		BackupRoot: config.BackupDir(d.ConfigDir),
		Note:       "tui " + sh.accountNote(sh.sourceAcc, p.SourceBucket.Key()) + " -> " + sh.accountNote(sh.destAcc, p.DestBucket.Key()),
		Progress:   func(ev apply.Event) { ch <- applyEventMsg{ev} },
	}
	keep := config.DefaultBackupRetention
	if d.Config != nil && d.Config.BackupRetention > 0 {
		keep = d.Config.BackupRetention
	}
	go func() {
		defer close(ch)
		res, err := apply.Run(p, opts)
		_, _ = backup.Prune(opts.BackupRoot, keep) // best effort
		if release != nil {
			release()
		}
		ch <- applyDoneMsg{res, err}
	}()
	return applyStartedMsg{ch}
}

// accountNote names an account for the backup note, without the "(current)"
// style suffix; fallback is used when the account is unknown.
func (sh *shared) accountNote(a *store.Account, fallback string) string {
	if a == nil {
		return fallback
	}
	n := sh.res.Display(a)
	for _, suf := range []string{" (current)", " (last signed in)"} {
		n = strings.TrimSuffix(n, suf)
	}
	return n
}

func waitFor(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (s *applyScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if s.err != "" {
			return s, nil
		}
		var cmd tea.Cmd
		s.spin, cmd = s.spin.Update(msg)
		return s, cmd
	case applyStartedMsg:
		s.ch = msg.ch
		return s, waitFor(s.ch)
	case applyLockErrMsg:
		s.finished = true
		s.err = msg.err.Error()
	case applyEventMsg:
		s.seen++
		s.log = append(s.log, s.logLine(msg.ev))
		return s, waitFor(s.ch)
	case applyDoneMsg:
		s.finished = true
		s.sh.quitRequested = s.quitRequested
		if msg.res == nil {
			msg.res = &apply.Result{}
		}
		s.sh.result, s.sh.applyErr = msg.res, msg.err
		return s, goTo(newDoneScreen(s.sh))
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" && s.blocksQuit() {
			s.quitRequested = true
			return s, nil
		}
		if s.err != "" && msg.String() == "esc" {
			return s, goTo(s.back)
		}
	}
	return s, nil
}

func (s *applyScreen) logLine(ev apply.Event) string {
	st := s.sh.st
	g, sty := st.g.add, st.ok
	switch ev.Status {
	case "merged", "overwritten":
		g, sty = st.g.merge, st.accent
	case "skipped":
		g, sty = st.g.same, st.dim
	case "failed":
		g, sty = st.g.orphan, st.bad
	case "restored":
		g, sty = st.g.restored, st.warn
	}
	title := ev.Op.Source.Title
	if title == "" {
		title = "(untitled)"
	}
	w := s.sh.width - 4
	status := " " + st.g.arrow + " " + ev.Status
	return sty.Render(g) + " " + st.fit(title, max(w-len(status)-2, 8)) + st.dim.Render(status)
}

func (s *applyScreen) View() string {
	sh, st := s.sh, s.sh.st
	var b strings.Builder
	b.WriteString(st.title.Render("Applying") + "\n" + st.ruleLine(sh.width) + "\n")
	if s.err != "" {
		b.WriteString(st.bad.Render(st.fit("Could not start: "+s.err, sh.width)) + "\n\n")
		b.WriteString("Another ccswitch may be running. Nothing was changed.\n")
		return b.String()
	}
	total := 0
	if sh.plan != nil {
		total = len(sh.plan.Ops)
	}
	b.WriteString(s.spin.View() + fmt.Sprintf(" working... %d of %d done\n", s.seen, total))
	if s.quitRequested {
		b.WriteString(st.warn.Render(st.fit(quitNotice, sh.width)) + "\n")
	}
	b.WriteString("\n")
	n := max(sh.avail-5, 3)
	from := max(len(s.log)-n, 0)
	for _, l := range s.log[from:] {
		b.WriteString(l + "\n")
	}
	return b.String()
}
