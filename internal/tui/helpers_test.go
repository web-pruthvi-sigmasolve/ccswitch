package tui

import (
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

const (
	accA = "aaaaaaaa-0000-4000-8000-000000000001" // source, labelled "work"
	accB = "bbbbbbbb-0000-4000-8000-000000000002" // destination, labelled "personal"
	accC = "cccccccc-0000-4000-8000-000000000003" // unlabelled, other org
	orgX = "11111111-1111-4111-8111-111111111111"
	orgY = "22222222-2222-4222-8222-222222222222"
	base = int64(1_760_000_000_000)
)

var testNow = time.UnixMilli(base + 10*3600*1000)

// fixture is a three-account store:
//
//	A/orgX (source): a1 alpha new, a2 alpha dest-newer, a3 beta source-newer,
//	                 a4 beta orphan (no transcript), a5 beta archived new
//	B/orgX (dest):   a2 (newer), a3 (older)
//	C/orgY:          c1
type fixture struct {
	*testutil.Fixture
	cfgDir string
	cfg    *config.Config
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{Fixture: testutil.NewFixtureStore(t), cfgDir: t.TempDir()}
	add := func(acc, org, id, title, cwd string, act int64, transcript, archived bool) {
		f.AddEntry(acc, org, testutil.Entry{
			SessionID: id, CLISessionID: "cli-" + id, Title: title, Cwd: cwd,
			LastActivityAt: act, IsArchived: archived,
		})
		if transcript {
			f.AddTranscript(cwd, "cli-"+id)
		}
	}
	add(accA, orgX, "local_a1", "Alpha one", "/w/alpha", base+1000, true, false)
	add(accA, orgX, "local_a2", "Alpha two", "/w/alpha", base+2000, true, false)
	add(accA, orgX, "local_a3", "Beta one", "/w/beta", base+3000, true, false)
	add(accA, orgX, "local_a4", "Beta orphan", "/w/beta", base+4000, false, false)
	add(accA, orgX, "local_a5", "Café ☕ archived", "/w/beta", base+5000, true, true)
	add(accB, orgX, "local_a2", "Alpha two (dest)", "/w/alpha", base+9000, true, false)
	add(accB, orgX, "local_a3", "Beta one", "/w/beta", base+100, true, false)
	add(accC, orgY, "local_c1", "Other org chat", "/w/other", base+50, true, false)
	f.WriteFile(f.UserData+"/config.json", []byte(fmt.Sprintf(`{"lastKnownAccountUuid":%q}`, accA)))
	f.cfg = &config.Config{Labels: map[string]string{accA: "work", accB: "personal"}, BackupRetention: 20}
	return f
}

func (f *fixture) discover(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Discover(store.Options{UserDataOverride: f.UserData, TranscriptRootOverride: f.TranscriptRoot})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (f *fixture) deps(t *testing.T) Deps {
	t.Helper()
	return Deps{
		Store: f.discover(t), Config: f.cfg, ConfigDir: f.cfgDir, Version: "test",
		Now: func() time.Time { return testNow },
	}
}

func (f *fixture) model(t *testing.T) Model {
	t.Helper()
	next, _ := New(f.deps(t)).Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	return next.(Model)
}

// key builds a key message from names such as "enter", "esc", "space", "a".
func kmsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// settle runs cmd and feeds the app's own messages back into the model until
// no commands remain. Timers (spinner, blink) are executed but not fed back.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 10000 {
			t.Fatal("settle: too many steps")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case gotoMsg, desktopMsg, applyStartedMsg, applyEventMsg, applyDoneMsg, applyLockErrMsg:
			next, c2 := m.Update(msg)
			m = next.(Model)
			queue = append(queue, c2)
		}
	}
	return m
}

// send delivers messages one by one and settles after each.
func send(t *testing.T, m Model, msgs ...tea.Msg) Model {
	t.Helper()
	for _, msg := range msgs {
		next, cmd := m.Update(msg)
		m = settle(t, next.(Model), cmd)
	}
	return m
}

// press sends named keys.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		m = send(t, m, kmsg(k))
	}
	return m
}

// chooseAccount moves the accounts cursor to uuid and presses enter.
func chooseAccount(t *testing.T, m Model, uuid string) Model {
	t.Helper()
	s := m.cur.(*accountsScreen)
	for i, a := range s.sh.store.Accounts {
		if a.UUID == uuid {
			s.cursor = i
		}
	}
	return press(t, m, "enter")
}

// toPick walks Accounts -> Direction -> Pick for A -> B.
func toPick(t *testing.T, m Model) Model {
	t.Helper()
	m = chooseAccount(t, m, accA)
	d, ok := m.cur.(*directionScreen)
	if !ok {
		t.Fatalf("expected direction screen, got %T", m.cur)
	}
	for i, a := range d.cands {
		if a.UUID == accB {
			d.cursor = i
		}
	}
	m = press(t, m, "enter")
	if _, ok := m.cur.(*pickScreen); !ok {
		t.Fatalf("expected pick screen, got %T", m.cur)
	}
	return m
}

// toReview selects everything on the pick screen and opens Review.
func toReview(t *testing.T, m Model) Model {
	t.Helper()
	m = press(t, toPick(t, m), "a", "down", "enter")
	if _, ok := m.cur.(*reviewScreen); !ok {
		t.Fatalf("expected review screen, got %T", m.cur)
	}
	return m
}
