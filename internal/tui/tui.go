// Package tui is the interactive terminal UI of ccswitch: a guided flow that
// moves Claude Code chats from one signed-in Claude Desktop account to another.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/apply"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/identity"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/plan"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
)

// minWidth is the narrowest terminal the screens can lay out.
const minWidth = 60

// Deps are the collaborators the TUI needs. Everything is injected so tests can
// run the UI against a fixture store.
type Deps struct {
	Store     *store.Store
	Config    *config.Config
	ConfigDir string // for Config.Save and config.BackupDir
	Resolver  *identity.Resolver
	Version   string
	// ASCII forces ASCII glyphs. They are also enabled automatically when
	// Config.ASCII is set, TERM=dumb, or on a legacy Windows console.
	ASCII bool
	// NoColor disables colour (NO_COLOR is honoured as well).
	NoColor bool
	// Lock is acquired right before applying; it may be nil in tests.
	Lock func() (release func(), err error)
	// Now is a test hook; the default is time.Now.
	Now func() time.Time
}

// screen is one step of the flow. Screens move around by returning a command
// that yields a gotoMsg.
type screen interface {
	Init() tea.Cmd
	Update(tea.Msg) (screen, tea.Cmd)
	View() string
}

// quitBlocker is implemented by screens that must not be interrupted by ctrl+c
// (an apply in progress).
type quitBlocker interface{ blocksQuit() bool }

// gotoMsg asks the root model to show another screen.
type gotoMsg struct{ s screen }

func goTo(s screen) tea.Cmd { return func() tea.Msg { return gotoMsg{s} } }

// shared is the state every screen reads and writes. It is owned by one Model.
type shared struct {
	d      Deps
	st     styles
	store  *store.Store
	res    *identity.Resolver
	width  int
	height int
	avail  int // lines available to the active screen (height minus help bar)

	sourceAcc, destAcc *store.Account
	source, dest       *store.Bucket
	selected           map[string]bool
	overrides          map[string]plan.Action
	opts               plan.Options
	optsGen            int // bumped when opts change, so the pick preview refreshes
	plan               *plan.Plan
	result             *apply.Result
	applyErr           error
	prompted           map[string]bool // accounts already offered a label this session
	quitRequested      bool            // ctrl+c arrived while applying
}

func (sh *shared) now() time.Time {
	if sh.d.Now != nil {
		return sh.d.Now()
	}
	return time.Now()
}

// display is the account's one-line description, made terminal-safe.
func (sh *shared) display(a *store.Account) string { return sh.st.text(sh.res.Display(a)) }

func (sh *shared) resetFlow() {
	sh.sourceAcc, sh.destAcc, sh.source, sh.dest = nil, nil, nil, nil
	sh.selected = map[string]bool{}
	sh.overrides = map[string]plan.Action{}
	sh.opts = plan.DefaultOptions()
	sh.opts.Overrides = sh.overrides
	sh.optsGen++
	sh.plan, sh.result, sh.applyErr = nil, nil, nil
}

// primaryBucket is the account's most active bucket (ties: most chats, then first).
func primaryBucket(a *store.Account) *store.Bucket {
	var best *store.Bucket
	for _, b := range a.Buckets {
		if best == nil || b.LastActivity() > best.LastActivity() ||
			(b.LastActivity() == best.LastActivity() && len(b.Entries) > len(best.Entries)) {
			best = b
		}
	}
	return best
}

// destBucketFor picks the destination bucket inside acc for a source in srcOrg:
// the same org when present, else the most active bucket, else a new bucket
// that apply will create.
func destBucketFor(acc *store.Account, srcOrg string) *store.Bucket {
	if b := acc.Bucket(srcOrg); b != nil {
		return b
	}
	if b := primaryBucket(acc); b != nil {
		return b
	}
	return &store.Bucket{AccountUUID: acc.UUID, OrgUUID: srcOrg, Path: filepath.Join(acc.Path, srcOrg)}
}

// selectedEntries returns the selected source entries in the source's order.
func (sh *shared) selectedEntries() []*store.Entry {
	var out []*store.Entry
	if sh.source == nil {
		return out
	}
	for _, e := range sh.source.Entries {
		if sh.selected[e.SessionID] {
			out = append(out, e)
		}
	}
	return out
}

// buildPlan plans the current selection with the current options.
func (sh *shared) buildPlan() (*plan.Plan, error) {
	sh.opts.Overrides = sh.overrides
	return plan.Build(sh.source, sh.dest, sh.selectedEntries(), sh.opts)
}

// sortedAccounts returns the store's accounts (already newest first) as a slice copy.
func sortedAccounts(st *store.Store, skip *store.Account) []*store.Account {
	var out []*store.Account
	for _, a := range st.Accounts {
		if a != skip {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActivity() > out[j].LastActivity() })
	return out
}

// Model is the root Bubble Tea model.
type Model struct {
	sh       *shared
	cur      screen
	help     help.Model
	showHelp bool
}

// New builds the root model, positioned on the Accounts screen.
func New(d Deps) Model {
	if d.Config == nil {
		d.Config = &config.Config{Labels: map[string]string{}}
	}
	if d.Resolver == nil {
		d.Resolver = identity.New(d.Store, d.Config)
	}
	ascii := detectASCII(runtime.GOOS, os.Getenv, d.ASCII || d.Config.ASCII)
	sh := &shared{
		d: d, st: newStyles(ascii, d.NoColor), store: d.Store, res: d.Resolver,
		width: 80, height: 24, prompted: map[string]bool{},
	}
	sh.resetFlow()
	sh.recalc(false)
	h := help.New()
	h.Styles.ShortKey = sh.st.accent
	h.Styles.FullKey = sh.st.accent
	h.Styles.ShortDesc, h.Styles.FullDesc = sh.st.dim, sh.st.dim
	h.Styles.ShortSeparator, h.Styles.FullSeparator = sh.st.dim, sh.st.dim
	h.Styles.Ellipsis = sh.st.dim
	h.ShortSeparator = " " + sh.st.g.sep + " "
	return Model{sh: sh, cur: newAccountsScreen(sh, 0), help: h}
}

// Run starts the TUI on the alternate screen and blocks until it exits.
func Run(d Deps) error {
	_, err := tea.NewProgram(New(d), tea.WithAltScreen()).Run()
	return err
}

func (sh *shared) recalc(showHelp bool) {
	lines := 2
	if showHelp {
		lines = 7
	}
	sh.avail = max(sh.height-lines, 5)
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return m.cur.Init() }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.sh.width, m.sh.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		m.sh.recalc(m.showHelp)
	case gotoMsg:
		m.cur = msg.s
		return m, m.cur.Init()
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if b, ok := m.cur.(quitBlocker); !ok || !b.blocksQuit() {
				return m, tea.Quit
			}
			// The screen decides what to do (the apply screen defers the quit).
			var cmd tea.Cmd
			m.cur, cmd = m.cur.Update(msg)
			return m, cmd
		}
		if c, ok := m.cur.(capturer); msg.String() == "?" && !(ok && c.capturing()) {
			m.showHelp = !m.showHelp
			m.sh.recalc(m.showHelp)
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.cur, cmd = m.cur.Update(msg)
	return m, cmd
}

// View implements tea.Model.
func (m Model) View() string {
	sh := m.sh
	if sh.width < minWidth {
		return sh.st.finalize(fmt.Sprintf("Please widen the terminal (at least %d columns, now %d).", minWidth, sh.width))
	}
	keys := helpKeys{short: []key.Binding{keyHelp, keyQuit}}
	if h, ok := m.cur.(helper); ok {
		keys = h.helpKeys()
	}
	m.help.ShowAll = m.showHelp
	body := m.cur.View() + "\n" + m.help.View(keys)
	return sh.st.finalize(body)
}
