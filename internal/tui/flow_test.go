package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ccswitch/ccswitch/internal/config"
	"github.com/ccswitch/ccswitch/internal/plan"
	"github.com/ccswitch/ccswitch/internal/testutil"
)

func TestPickGroupsByProjectNewestFirst(t *testing.T) {
	m := toPick(t, newFixture(t).model(t))
	p := m.cur.(*pickScreen)
	if len(p.groups) != 2 || p.groups[0].name != "beta" || p.groups[1].name != "alpha" {
		t.Fatalf("groups: %+v", p.groups)
	}
	if got := p.groups[0].entries[0].SessionID; got != "local_a5" {
		t.Errorf("newest chat first expected, got %s", got)
	}
	// header, 3 beta chats, header, 2 alpha chats
	if len(p.rows) != 7 || p.rows[0].e != nil || p.rows[4].e != nil {
		t.Errorf("rows: %d", len(p.rows))
	}
	v := m.View()
	for _, want := range []string{"beta (3)", "alpha (2)", "(archived)", "FROM work", "TO personal"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestPickToggleSelectAllNoneAndCollapse(t *testing.T) {
	m := toPick(t, newFixture(t).model(t))
	p := m.cur.(*pickScreen)
	m = press(t, m, "down", "space")
	if !m.sh.selected["local_a5"] || len(m.sh.selected) != 1 {
		t.Errorf("space should select the cursor chat: %v", m.sh.selected)
	}
	m = press(t, m, "space")
	if len(m.sh.selected) != 0 {
		t.Error("space again should unselect")
	}
	m = press(t, m, "a")
	if len(m.sh.selected) != 5 {
		t.Errorf("a: got %d selected", len(m.sh.selected))
	}
	m = press(t, m, "n")
	if len(m.sh.selected) != 0 {
		t.Error("n should clear")
	}
	// enter on the header toggles the whole group; space does too.
	m = press(t, m, "up", "enter")
	if len(m.sh.selected) != 3 {
		t.Errorf("group select: %v", m.sh.selected)
	}
	m = press(t, m, "enter")
	if len(m.sh.selected) != 0 {
		t.Error("second enter should unselect the group")
	}
	// p collapses the group and the cursor lands on its header.
	m = press(t, m, "down", "p")
	if len(p.rows) != 4 || p.rows[p.cursor].e != nil {
		t.Errorf("collapse: rows=%d cursor=%d", len(p.rows), p.cursor)
	}
	m = press(t, m, "p")
	if len(p.rows) != 7 {
		t.Errorf("expand: rows=%d", len(p.rows))
	}
	// tab / shift+tab jump between group headers.
	m = press(t, m, "tab")
	if p.cursor != 4 {
		t.Errorf("tab: cursor=%d", p.cursor)
	}
	m = press(t, m, "shift+tab")
	if p.cursor != 0 {
		t.Errorf("shift+tab: cursor=%d", p.cursor)
	}
}

func TestPickFilterNarrows(t *testing.T) {
	m := toPick(t, newFixture(t).model(t))
	p := m.cur.(*pickScreen)
	m = press(t, m, "/")
	if !p.capturing() {
		t.Fatal("filter box should capture keys")
	}
	for _, r := range "ALPHA" {
		m = press(t, m, string(r))
	}
	if len(p.rows) != 3 {
		t.Fatalf("project filter: rows=%d", len(p.rows))
	}
	m = press(t, m, "enter") // keep the filter, leave the box
	m = press(t, m, "a")
	if len(m.sh.selected) != 2 {
		t.Errorf("a selects only matching chats, got %v", m.sh.selected)
	}
	m = press(t, m, "esc") // clears the filter first
	if p.query != "" || len(p.rows) != 7 {
		t.Errorf("esc should clear the filter: %q rows=%d", p.query, len(p.rows))
	}
	m = press(t, m, "n", "/")
	for _, r := range "orphan" {
		m = press(t, m, string(r))
	}
	if len(p.rows) != 2 || p.rows[1].e.SessionID != "local_a4" {
		t.Errorf("title filter: %d rows", len(p.rows))
	}
	m = press(t, m, "esc")
	if p.filtering || p.query != "" {
		t.Error("esc in the box cancels the filter")
	}
	if _, ok := press(t, m, "esc").cur.(*directionScreen); !ok {
		t.Error("esc outside the box goes back")
	}
}

func TestPickStatusMatchesPreviewPlan(t *testing.T) {
	m := toPick(t, newFixture(t).model(t))
	p := m.cur.(*pickScreen)
	want := map[string]string{
		"local_a1": "new", "local_a2": "dest newer", "local_a3": "newer",
		"local_a4": "no transcript", "local_a5": "new",
	}
	classes := map[string]plan.Class{
		"local_a1": plan.Missing, "local_a2": plan.DestNewer, "local_a3": plan.SourceNewer,
		"local_a4": plan.Orphan, "local_a5": plan.Missing,
	}
	for _, e := range m.sh.source.Entries {
		_, label, _ := p.status(e)
		if label != want[e.SessionID] {
			t.Errorf("%s: label %q want %q", e.SessionID, label, want[e.SessionID])
		}
		if p.preview[e.SessionID].Class != classes[e.SessionID] {
			t.Errorf("%s: class %v", e.SessionID, p.preview[e.SessionID].Class)
		}
	}
	for _, w := range []string{"new", "newer", "dest newer", "no transcript"} {
		if !strings.Contains(m.View(), w) {
			t.Errorf("view lacks status %q", w)
		}
	}
}

func TestPickCycleOverrideOnlyForConflicts(t *testing.T) {
	m := toPick(t, newFixture(t).model(t))
	p := m.cur.(*pickScreen)
	for i, r := range p.rows {
		if r.e != nil && r.e.SessionID == "local_a2" {
			p.cursor = i
		}
	}
	steps := []struct {
		has bool
		act plan.Action
	}{{true, plan.Skip}, {true, plan.Merge}, {false, 0}}
	for i, s := range steps {
		m = press(t, m, "c")
		got, has := m.sh.overrides["local_a2"]
		if has != s.has || (has && got != s.act) {
			t.Errorf("step %d: %v %v", i, got, has)
		}
		if i == 0 && !strings.Contains(m.View(), "[keep]") {
			t.Error("override tag missing")
		}
	}
	p.cursor = 1 // a5: no conflict
	m = press(t, m, "c")
	if len(m.sh.overrides) != 0 || !strings.Contains(m.View(), "Only chats") {
		t.Error("c on a non-conflict row must be refused with a note")
	}
}

func TestPickEnterNeedsSelection(t *testing.T) {
	m := press(t, toPick(t, newFixture(t).model(t)), "down", "enter")
	if _, ok := m.cur.(*pickScreen); !ok || !strings.Contains(m.View(), "Select at least one") {
		t.Errorf("expected a hint:\n%s", m.View())
	}
}

func TestReviewSummaryAndPolicyCycling(t *testing.T) {
	m := toReview(t, newFixture(t).model(t))
	p := m.sh.plan
	if p.Adds != 2 || p.Merges != 1 || p.Conflicts != 1 || p.Orphans != 1 {
		t.Fatalf("plan: %s", p.Summary())
	}
	v := m.View()
	for _, want := range []string{"2 to add", "1 to merge", "1 kept", "1 orphans skipped", "mode: merge", "keep-dest",
		"To add", "Kept as they are", "1 skipped (press s to show)", "Backup", "differs:"} {
		if !strings.Contains(v, want) {
			t.Errorf("review lacks %q:\n%s", want, v)
		}
	}
	m = press(t, m, "c") // prefer-source
	if m.sh.plan.Merges != 2 || m.sh.plan.Conflicts != 0 {
		t.Errorf("prefer-source: %s", m.sh.plan.Summary())
	}
	m = press(t, m, "c") // skip
	if m.sh.plan.Skips != 2 {
		t.Errorf("skip policy: %s", m.sh.plan.Summary())
	}
	m = press(t, m, "c", "m")
	if m.sh.opts.Mode != plan.ModeOverwrite || m.sh.plan.Overwrites != 1 {
		t.Errorf("mode cycle: %v %s", m.sh.opts.Mode, m.sh.plan.Summary())
	}
	m = press(t, m, "m", "m", "o")
	if m.sh.opts.Mode != plan.ModeMerge || !m.sh.opts.IncludeOrphans || m.sh.plan.Adds != 3 {
		t.Errorf("orphans: %s", m.sh.plan.Summary())
	}
	m = press(t, m, "s")
	if !strings.Contains(m.View(), "Alpha") {
		t.Error("s should expand skipped list or at least keep chats listed")
	}
	// esc returns to the same pick screen and keeps the selection.
	m = press(t, m, "esc")
	if _, ok := m.cur.(*pickScreen); !ok || len(m.sh.selected) != 5 {
		t.Errorf("back to pick: %T %d", m.cur, len(m.sh.selected))
	}
}

func TestReviewDisabledWhenNothingChanges(t *testing.T) {
	m := toPick(t, newFixture(t).model(t))
	p := m.cur.(*pickScreen)
	for i, r := range p.rows {
		if r.e != nil && r.e.SessionID == "local_a4" {
			p.cursor = i
		}
	}
	m = press(t, m, "space", "enter")
	r, ok := m.cur.(*reviewScreen)
	if !ok {
		t.Fatalf("got %T", m.cur)
	}
	if !strings.Contains(m.View(), "Nothing to apply") {
		t.Errorf("reason missing:\n%s", m.View())
	}
	m = press(t, m, "enter")
	if m.cur != screen(r) {
		t.Errorf("enter must be disabled, got %T", m.cur)
	}
}

func TestReviewWarnsAboutDesktopAndOrgs(t *testing.T) {
	m := toReview(t, newFixture(t).model(t))
	m = send(t, m, desktopMsg{running: true})
	if !strings.Contains(m.View(), "Claude Desktop is running") {
		t.Error("desktop note missing")
	}
	if strings.Contains(m.View(), "Different organization") {
		t.Error("same org must not warn")
	}
	f := newFixture(t)
	m = chooseAccount(t, f.model(t), accA)
	d := m.cur.(*directionScreen)
	for i, a := range d.cands {
		if a.UUID == accC {
			d.cursor = i
		}
	}
	m = press(t, m, "enter", "a", "down", "enter")
	if !strings.Contains(m.View(), "Different organization") {
		t.Errorf("cross-org warning missing:\n%s", m.View())
	}
}

func TestApplyRunsRealPlanAndDoneShowsBackup(t *testing.T) {
	f := newFixture(t)
	m := toReview(t, f.model(t))
	locked, released := 0, 0
	m.sh.d.Lock = func() (func(), error) { locked++; return func() { released++ }, nil }
	m = press(t, m, "c") // prefer-source: dest-newer a2 is merged too
	m = press(t, m, "enter")
	done, ok := m.cur.(*doneScreen)
	if !ok {
		t.Fatalf("expected done screen, got %T", m.cur)
	}
	r := m.sh.result
	if r.Applied != 4 || r.Failed != 0 || r.Skipped != 1 || r.BackupSet == nil {
		t.Fatalf("result: %+v", r)
	}
	if locked != 1 || released != 1 {
		t.Errorf("lock/release: %d/%d", locked, released)
	}
	v := done.View()
	for _, want := range []string{"applied 4", "failed 0", "skipped 1", "Backup " + r.BackupSet.ID, r.BackupSet.Dir, "restart"} {
		if !strings.Contains(v, want) {
			t.Errorf("done lacks %q:\n%s", want, v)
		}
	}
	for _, id := range []string{"local_a1", "local_a5"} {
		if _, err := os.Stat(filepath.Join(f.UserData, "claude-code-sessions", accB, orgX, id+".json")); err != nil {
			t.Errorf("%s not copied: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(r.BackupSet.Dir, "manifest.json")); err != nil {
		t.Errorf("backup manifest: %v", err)
	}
	// r starts again with a fresh store that now knows the new chats.
	m = press(t, m, "r")
	acc, ok := m.cur.(*accountsScreen)
	if !ok {
		t.Fatalf("restart: %T", m.cur)
	}
	if b := acc.sh.store.Account(accB).Bucket(orgX); len(b.Entries) != 4 {
		t.Errorf("store not rediscovered: %d entries", len(b.Entries))
	}
}

func TestDoneWithoutOverwriteNeedsNoBackup(t *testing.T) {
	f := newFixture(t)
	m := toPick(t, f.model(t))
	p := m.cur.(*pickScreen)
	for i, r := range p.rows {
		if r.e != nil && r.e.SessionID == "local_a1" {
			p.cursor = i
		}
	}
	m = press(t, m, "space", "enter", "enter")
	if m.sh.result == nil || m.sh.result.Applied != 1 || m.sh.result.BackupSet != nil {
		t.Fatalf("result: %+v", m.sh.result)
	}
	if !strings.Contains(m.View(), "no backup was needed") {
		t.Errorf("done view:\n%s", m.View())
	}
	if _, cmd := m.Update(kmsg("q")); cmd == nil {
		t.Error("q should quit")
	}
}

func TestApplyLockErrorIsShownAndEscReturns(t *testing.T) {
	m := toReview(t, newFixture(t).model(t))
	m.sh.d.Lock = func() (func(), error) { return nil, errors.New("another ccswitch is running") }
	m = press(t, m, "enter")
	a, ok := m.cur.(*applyScreen)
	if !ok || !strings.Contains(m.View(), "another ccswitch is running") {
		t.Fatalf("%T:\n%s", m.cur, m.View())
	}
	m = press(t, m, "esc")
	if _, ok := m.cur.(*reviewScreen); !ok || a.err == "" {
		t.Errorf("esc should return to review, got %T", m.cur)
	}
}

func TestPickHandlesTwoThousandChats(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	for i := 0; i < 2000; i++ {
		f.AddEntry(accA, orgX, testutil.Entry{
			SessionID: fmt.Sprintf("local_%04d", i), CLISessionID: fmt.Sprintf("cli-%04d", i),
			Title: fmt.Sprintf("Chat number %d with a fairly long title to truncate %s", i, strings.Repeat("x", 80)),
			Cwd:   fmt.Sprintf("/w/proj%02d", i%40), LastActivityAt: base + int64(i)*1000,
		})
	}
	f.AddBucket(accB, orgX)
	nf := &fixture{Fixture: f, cfgDir: t.TempDir()}
	nf.cfg = &config.Config{Labels: map[string]string{accA: "work", accB: "personal"}}
	m := toPick(t, nf.model(t))
	p := m.cur.(*pickScreen)
	if len(p.rows) != 2040 {
		t.Fatalf("rows=%d", len(p.rows))
	}
	m = send(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})
	for i := 0; i < 200; i++ {
		m = press(t, m, "down")
	}
	m = press(t, m, "pgdown", "pgdown", "space", "a")
	if len(m.sh.selected) != 2000 {
		t.Errorf("selected %d", len(m.sh.selected))
	}
	lines := strings.Count(m.View(), "\n") + 1
	if lines > 30 {
		t.Errorf("view has %d lines for a 30-line terminal", lines)
	}
	if p.cursor < p.offset || p.cursor >= p.offset+p.pageSize() {
		t.Errorf("cursor %d outside window [%d,%d)", p.cursor, p.offset, p.offset+p.pageSize())
	}
}
