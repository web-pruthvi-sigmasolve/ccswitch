package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAccountsScreenListsAccountsAndMarksCurrent(t *testing.T) {
	f := newFixture(t)
	m := f.model(t)
	v := m.View()
	for _, want := range []string{"work (current)", "personal", "5 chats", "account cccccccc", "Alpha two"} {
		if !strings.Contains(v, want) {
			t.Errorf("accounts view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "personal (current)") {
		t.Error("only the current account may carry the marker")
	}
}

func TestSingleAccountExplainsAndQuitsOnAnyKey(t *testing.T) {
	f := newFixture(t)
	d := f.deps(t)
	d.Store.Accounts = d.Store.Accounts[:1]
	m := New(d)
	if v := m.View(); !strings.Contains(v, "doctor") || !strings.Contains(v, "two") {
		t.Errorf("missing explanation:\n%s", v)
	}
	_, cmd := m.Update(kmsg("x"))
	if cmd == nil || cmd() != tea.Msg(tea.QuitMsg{}) {
		t.Error("any key should quit")
	}
}

func TestDirectionExcludesSourceAndWarnsAcrossOrgs(t *testing.T) {
	f := newFixture(t)
	m := chooseAccount(t, f.model(t), accA)
	d, ok := m.cur.(*directionScreen)
	if !ok {
		t.Fatalf("got %T", m.cur)
	}
	if len(d.cands) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(d.cands))
	}
	for _, a := range d.cands {
		if a.UUID == accA {
			t.Error("source listed as destination")
		}
	}
	if !strings.Contains(m.View(), "FROM work") {
		t.Errorf("header missing:\n%s", m.View())
	}
	for i, a := range d.cands {
		d.cursor = i
		warned := strings.Contains(m.View(), "Different organization")
		if want := a.UUID == accC; warned != want {
			t.Errorf("account %s: warning shown=%v want %v", a.UUID, warned, want)
		}
	}
	// esc goes back to the accounts list.
	if _, ok := press(t, m, "esc").cur.(*accountsScreen); !ok {
		t.Error("esc should return to accounts")
	}
}

func TestDestinationBucketChoice(t *testing.T) {
	f := newFixture(t)
	st := f.discover(t)
	b := destBucketFor(st.Account(accB), orgX)
	if b.OrgUUID != orgX || len(b.Entries) != 2 {
		t.Errorf("same-org bucket expected, got %+v", b)
	}
	if got := destBucketFor(st.Account(accC), orgX); got.OrgUUID != orgY {
		t.Errorf("most active bucket expected, got %s", got.OrgUUID)
	}
	empty := st.Account(accB)
	cp := *empty
	cp.Buckets = nil
	nb := destBucketFor(&cp, orgX)
	if nb.OrgUUID != orgX || nb.Path != filepath.Join(cp.Path, orgX) || nb.AccountUUID != accB {
		t.Errorf("new bucket expected, got %+v", nb)
	}
}

func TestUnlabelledSourceOffersLabelAndSavesIt(t *testing.T) {
	f := newFixture(t)
	m := chooseAccount(t, f.model(t), accC)
	if _, ok := m.cur.(*labelScreen); !ok {
		t.Fatalf("expected label screen, got %T", m.cur)
	}
	m = press(t, m, "o", "t", "h", "e", "r", "enter")
	if f.cfg.Labels[accC] != "other" {
		t.Errorf("label not set: %v", f.cfg.Labels)
	}
	if _, err := os.Stat(filepath.Join(f.cfgDir, "config.json")); err != nil {
		t.Errorf("config not saved: %v", err)
	}
	if _, ok := m.cur.(*directionScreen); !ok {
		t.Errorf("expected direction after saving, got %T", m.cur)
	}
}

func TestLabelEscSkipsAndLKeyEditsFromAccounts(t *testing.T) {
	f := newFixture(t)
	m := press(t, chooseAccount(t, f.model(t), accC), "esc")
	if _, ok := m.cur.(*directionScreen); !ok {
		t.Fatalf("esc should skip to direction, got %T", m.cur)
	}
	if _, set := f.cfg.Labels[accC]; set {
		t.Error("esc must not save a label")
	}
	m = press(t, f.model(t), "l")
	if _, ok := m.cur.(*labelScreen); !ok {
		t.Fatalf("l should open the label screen, got %T", m.cur)
	}
	if _, ok := press(t, m, "esc").cur.(*accountsScreen); !ok {
		t.Error("esc should return to accounts")
	}
}

func TestLabelSaveErrorShownInline(t *testing.T) {
	f := newFixture(t)
	d := f.deps(t)
	d.ConfigDir = ""
	m := press(t, New(d), "l", "x", "enter")
	if _, ok := m.cur.(*labelScreen); !ok || !strings.Contains(m.View(), "Could not save") {
		t.Errorf("expected inline error:\n%s", m.View())
	}
}

func TestZeroChatAccountCannotBeSource(t *testing.T) {
	f := newFixture(t)
	f.AddAccount("dddddddd-0000-4000-8000-000000000004")
	m := f.model(t)
	for i, a := range m.sh.store.Accounts {
		if a.UUID == "dddddddd-0000-4000-8000-000000000004" {
			m.cur.(*accountsScreen).cursor = i
		}
	}
	m = press(t, m, "enter")
	if _, ok := m.cur.(*accountsScreen); !ok || !strings.Contains(m.View(), "no chats") {
		t.Errorf("should stay with a notice:\n%s", m.View())
	}
}

func TestCtrlCQuitsFromEveryScreen(t *testing.T) {
	f := newFixture(t)
	for name, m := range map[string]Model{
		"accounts": f.model(t),
		"pick":     toPick(t, f.model(t)),
		"review":   toReview(t, f.model(t)),
	} {
		_, cmd := m.Update(kmsg("ctrl+c"))
		if cmd == nil || cmd() != tea.Msg(tea.QuitMsg{}) {
			t.Errorf("%s: ctrl+c did not quit", name)
		}
	}
	// Also while typing in the filter and the label box.
	m := press(t, toPick(t, f.model(t)), "/")
	if _, cmd := m.Update(kmsg("ctrl+c")); cmd == nil || cmd() != tea.Msg(tea.QuitMsg{}) {
		t.Error("ctrl+c must quit while filtering")
	}
}

func TestNarrowTerminalAsksToWiden(t *testing.T) {
	m := send(t, newFixture(t).model(t), tea.WindowSizeMsg{Width: 40, Height: 20})
	if v := m.View(); !strings.Contains(v, "widen") {
		t.Errorf("got %q", v)
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if strings.Contains(m.View(), "widen") {
		t.Error("message should vanish once wide enough")
	}
}

func TestHelpToggle(t *testing.T) {
	m := newFixture(t).model(t)
	short := m.View()
	m = press(t, m, "?")
	if !m.showHelp || len(m.View()) <= len(short) && m.View() == short {
		t.Error("help should expand")
	}
	// "?" is text inside the filter box and must not toggle help.
	p := press(t, toPick(t, newFixture(t).model(t)), "/", "?")
	if p.showHelp {
		t.Error("? typed in the filter toggled help")
	}
}

func TestASCIIModeProducesOnlyASCII(t *testing.T) {
	f := newFixture(t)
	d := f.deps(t)
	d.ASCII = true
	m := New(d)
	check := func(stage string) {
		t.Helper()
		for _, l := range []string{m.View()} {
			for i := 0; i < len(l); i++ {
				if l[i] >= 0x80 {
					t.Fatalf("%s: non-ASCII byte at %d in:\n%s", stage, i, l)
				}
			}
		}
	}
	check("accounts")
	m = press(t, m, "?")
	check("accounts+help")
	m = press(t, m, "?")
	m = chooseAccount(t, m, accA)
	check("direction")
	m = toPick(t, New(d))
	m = press(t, m, "a")
	check("pick")
	m = press(t, m, "down", "enter")
	check("review")
	m = press(t, m, "s")
	check("review+skipped")
	m = press(t, m, "enter")
	check("done")
	if !strings.Contains(m.View(), "applied") {
		t.Errorf("done view:\n%s", m.View())
	}
}

func TestDetectASCII(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	cases := []struct {
		goos   string
		env    map[string]string
		forced bool
		want   bool
	}{
		{"linux", nil, false, false},
		{"linux", nil, true, true},
		{"linux", map[string]string{"TERM": "dumb"}, false, true},
		{"windows", nil, false, true},
		{"windows", map[string]string{"WT_SESSION": "x"}, false, false},
		{"windows", map[string]string{"ConEmuANSI": "ON"}, false, false},
		{"darwin", nil, false, false},
	}
	for _, c := range cases {
		if got := detectASCII(c.goos, env(c.env), c.forced); got != c.want {
			t.Errorf("%s %v forced=%v: got %v", c.goos, c.env, c.forced, got)
		}
	}
}

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		30 * time.Second: "just now", 5 * time.Minute: "5m ago", 2 * time.Hour: "2h ago",
		72 * time.Hour: "3d ago", 21 * 24 * time.Hour: "3w ago", 90 * 24 * time.Hour: "3mo ago", 800 * 24 * time.Hour: "2y ago",
	} {
		if got := relTime(now, now.Add(-d)); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
	if relTime(now, time.Time{}) != "never" {
		t.Error("zero time should read never")
	}
}
