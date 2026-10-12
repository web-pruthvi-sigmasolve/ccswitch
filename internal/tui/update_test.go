package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type fakeUpdate struct {
	cached, result UpdateStatus
	cachedOK       bool
	resultOK       bool
	waited         time.Duration
}

func (f *fakeUpdate) Cached() (UpdateStatus, bool) { return f.cached, f.cachedOK }
func (f *fakeUpdate) Result(w time.Duration) (UpdateStatus, bool) {
	f.waited = w
	return f.result, f.resultOK
}

var newer = UpdateStatus{Current: "0.1.0", Latest: "0.2.0", Available: true, URL: "https://example.test/rel/0.2.0"}

const noticeText = "ccswitch 0.2.0 available"

func updModel(t *testing.T, src UpdateSource, ascii bool) Model {
	t.Helper()
	f := newFixture(t)
	d := f.deps(t)
	d.Update = src
	d.ASCII = ascii
	m := New(d)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	m = next.(Model)
	for _, um := range initUpdateMsgs(m.Init()) {
		next, _ := m.Update(um)
		m = next.(Model)
	}
	return m
}

func TestUpdateNoticeAbsentWithoutSource(t *testing.T) {
	m := updModel(t, nil, false)
	if strings.Contains(m.View(), noticeText) {
		t.Fatal("notice shown without a source")
	}
	m = press(t, m, "u")
	if strings.Contains(m.View(), "Release:") {
		t.Fatal("detail box opened without a source")
	}
}

func TestUpdateNoticeAbsentWhenNotAvailable(t *testing.T) {
	src := &fakeUpdate{cached: UpdateStatus{Latest: "0.1.0"}, cachedOK: true}
	m := updModel(t, src, false)
	if strings.Contains(m.View(), noticeText) {
		t.Fatal("notice shown although no update is available")
	}
}

func TestUpdateNoticeOnAccountsAndDoneNotApply(t *testing.T) {
	m := updModel(t, &fakeUpdate{cached: newer, cachedOK: true}, false)
	if !strings.Contains(m.View(), "↑ "+noticeText+" · run: ccswitch update") {
		t.Fatalf("notice missing on accounts:\n%s", m.View())
	}
	// Notice sits directly above the help bar.
	lines := strings.Split(strings.TrimRight(m.View(), "\n"), "\n")
	if !strings.Contains(lines[len(lines)-2], noticeText) {
		t.Fatalf("notice is not directly above the help bar: %q", lines[len(lines)-2])
	}
	done := newDoneScreen(m.sh)
	m.cur = done
	if !strings.Contains(m.View(), noticeText) {
		t.Fatal("notice missing on done")
	}
	m.cur = &applyScreen{sh: m.sh}
	if strings.Contains(m.View(), noticeText) {
		t.Fatal("notice shown on apply")
	}
}

func TestUpdateNoticeAppearsAfterResult(t *testing.T) {
	src := &fakeUpdate{result: newer, resultOK: true}
	f := newFixture(t)
	d := f.deps(t)
	d.Update = src
	m := New(d)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	m = next.(Model)
	if strings.Contains(m.View(), noticeText) {
		t.Fatal("notice present before the result")
	}
	cmd := m.Init()
	msgs := initUpdateMsgs(cmd)
	if len(msgs) != 1 {
		t.Fatal("Init did not produce one updateMsg")
	}
	got := &msgs[0]
	if src.waited != 3*time.Second {
		t.Fatalf("waited %v, want 3s", src.waited)
	}
	next, _ = m.Update(*got)
	if !strings.Contains(next.(Model).View(), noticeText) {
		t.Fatal("notice missing after updateMsg")
	}
}

func TestUpdateNoticeASCIIAndWidth(t *testing.T) {
	m := updModel(t, &fakeUpdate{cached: newer, cachedOK: true}, true)
	v := m.View()
	if !strings.Contains(v, "^ "+noticeText+" - run: ccswitch update") {
		t.Fatalf("ascii notice missing:\n%s", v)
	}
	for _, r := range v {
		if r > 127 {
			t.Fatalf("non-ASCII rune %q in ascii view", r)
		}
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 50})
	m = next.(Model)
	m.sh.update.Latest = strings.Repeat("9", 100)
	for _, l := range strings.Split(m.View(), "\n") {
		if len([]rune(l)) > 60 {
			t.Fatalf("line wider than terminal: %q", l)
		}
	}
}

func TestUpdateKeyTogglesDetailBox(t *testing.T) {
	m := updModel(t, &fakeUpdate{cached: newer, cachedOK: true}, false)
	m = press(t, m, "u")
	v := m.View()
	for _, want := range []string{newer.URL, "ccswitch update", "next version"} {
		if !strings.Contains(v, want) {
			t.Fatalf("detail box lacks %q:\n%s", want, v)
		}
	}
	if _, ok := m.cur.(*accountsScreen); !ok {
		t.Fatal("u changed screen")
	}
	m = press(t, m, "esc")
	if strings.Contains(m.View(), "Release:") {
		t.Fatal("esc did not close the box")
	}
	m = press(t, m, "u", "u")
	if strings.Contains(m.View(), "Release:") {
		t.Fatal("u did not toggle the box closed")
	}
}

func TestUpdateCtrlCStillQuits(t *testing.T) {
	m := updModel(t, &fakeUpdate{cached: newer, cachedOK: true}, false)
	m = press(t, m, "u")
	_, cmd := m.Update(kmsg("ctrl+c"))
	if cmd == nil {
		t.Fatal("ctrl+c returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c did not quit")
	}
}

// initUpdateMsgs runs Init's command and returns the updateMsgs it yields.
func initUpdateMsgs(cmd tea.Cmd) []updateMsg {
	var out []updateMsg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch msg := c().(type) {
		case tea.BatchMsg:
			for _, cc := range msg {
				run(cc)
			}
		case updateMsg:
			out = append(out, msg)
		}
	}
	run(cmd)
	return out
}
