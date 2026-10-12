package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/apply"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/backup"
)

// isQuit reports whether cmd produces tea.QuitMsg.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// runningApply puts the model on an apply screen that has not finished.
func runningApply(t *testing.T) Model {
	t.Helper()
	m := toReview(t, newFixture(t).model(t))
	m.cur = newApplyScreen(m.sh, m.cur)
	return m
}

func TestCtrlCDuringApplyDefersQuit(t *testing.T) {
	m := runningApply(t)
	next, cmd := m.Update(kmsg("ctrl+c"))
	m = next.(Model)
	if isQuit(cmd) {
		t.Fatal("ctrl+c must not quit while applying")
	}
	if !strings.Contains(m.View(), quitNotice) {
		t.Errorf("notice missing:\n%s", m.View())
	}
	// A second ctrl+c is still deferred.
	if _, cmd := m.Update(kmsg("ctrl+c")); isQuit(cmd) {
		t.Fatal("second ctrl+c must not quit either")
	}
}

func TestDoneAfterQuitRequestShowsBackupSet(t *testing.T) {
	m := runningApply(t)
	next, _ := m.Update(kmsg("ctrl+c"))
	m = next.(Model)
	res := &apply.Result{Applied: 1, BackupSet: &backup.Set{ID: "20250101-000000", Dir: "/tmp/b", Files: []backup.FileRecord{{}}}}
	m = send(t, m, applyDoneMsg{res: res})
	if _, ok := m.cur.(*doneScreen); !ok {
		t.Fatalf("expected done screen, got %T", m.cur)
	}
	v := m.View()
	if !strings.Contains(v, "20250101-000000") || !strings.Contains(v, "ctrl+c") {
		t.Errorf("done view:\n%s", v)
	}
	// Now quitting is allowed.
	if !isQuit(func() tea.Msg { _, c := m.Update(kmsg("ctrl+c")); return c() }) {
		t.Error("ctrl+c on the done screen should quit")
	}
}

func TestCtrlCQuitsWhenNotApplying(t *testing.T) {
	m := newFixture(t).model(t)
	if _, cmd := m.Update(kmsg("ctrl+c")); !isQuit(cmd) {
		t.Error("ctrl+c on accounts should quit")
	}
}
