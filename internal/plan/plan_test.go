package plan

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ccswitch/ccswitch/internal/store"
)

func entry(dir, id string, activity, focused int64, extra string) *store.Entry {
	raw := fmt.Sprintf(`{"sessionId":%q,"title":"t","lastActivityAt":%d,"lastFocusedAt":%d%s}`, id, activity, focused, extra)
	return &store.Entry{
		SessionID: id, Path: filepath.Join(dir, id+".json"), Raw: []byte(raw),
		LastActivityAt: activity, LastFocusedAt: focused, TranscriptExists: true,
	}
}

func bucket(acct, org string, es ...*store.Entry) *store.Bucket {
	return &store.Bucket{AccountUUID: acct, OrgUUID: org, Path: "/s/" + acct + "/" + org, Entries: es}
}

func opts() Options { return DefaultOptions() }

func build(t *testing.T, src, dst *store.Bucket, o Options) *Plan {
	t.Helper()
	p, err := Build(src, dst, src.Entries, o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClasses(t *testing.T) {
	tests := []struct {
		name      string
		src, dst  *store.Entry
		mut       func(*store.Entry)
		wantClass Class
		wantAct   Action
	}{
		{"missing", entry("/a", "local_1", 5, 5, ""), nil, nil, Missing, Add},
		{"identical", entry("/a", "local_1", 5, 5, ""), entry("/b", "local_1", 5, 5, ""), nil, Identical, Skip},
		{"source newer", entry("/a", "local_1", 9, 5, ""), entry("/b", "local_1", 5, 5, ""), nil, SourceNewer, Merge},
		{"dest newer", entry("/a", "local_1", 5, 5, ""), entry("/b", "local_1", 9, 5, ""), nil, DestNewer, Skip},
		{"meta only", entry("/a", "local_1", 5, 5, ""), entry("/b", "local_1", 5, 8, ""), nil, MetaOnly, Skip},
		{"equal activity source-owned diff, src focus newer", entry("/a", "local_1", 5, 9, `,"model":"a"`), entry("/b", "local_1", 5, 8, `,"model":"b"`), nil, SourceNewer, Merge},
		{"equal activity source-owned diff, dst focus newer", entry("/a", "local_1", 5, 7, `,"model":"a"`), entry("/b", "local_1", 5, 8, `,"model":"b"`), nil, DestNewer, Skip},
		{"orphan", entry("/a", "local_1", 5, 5, ""), nil, func(e *store.Entry) { e.TranscriptExists = false }, Orphan, Skip},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mut != nil {
				tc.mut(tc.src)
			}
			src := bucket("A", "o", tc.src)
			dst := bucket("B", "o")
			if tc.dst != nil {
				dst.Entries = append(dst.Entries, tc.dst)
			}
			p := build(t, src, dst, opts())
			op := p.Ops[0]
			if op.Class != tc.wantClass || op.Action != tc.wantAct {
				t.Fatalf("got %v/%v (%s), want %v/%v", op.Class, op.Action, op.Reason, tc.wantClass, tc.wantAct)
			}
			if op.Reason == "" {
				t.Fatal("empty reason")
			}
			if (tc.dst == nil) != (op.Dest == nil) {
				t.Fatal("Dest pointer wrong")
			}
		})
	}
}

func TestModeConflictMatrix(t *testing.T) {
	srcNewer := map[Mode]Action{ModeMerge: Merge, ModeOverwrite: Overwrite, ModeAddOnly: Skip}
	for _, m := range []Mode{ModeMerge, ModeOverwrite, ModeAddOnly} {
		for _, c := range []OnConflict{KeepDest, PreferSource, SkipConflicts} {
			t.Run(fmt.Sprintf("%v/%v", m, c), func(t *testing.T) {
				o := opts()
				o.Mode, o.OnConflict = m, c
				// SourceNewer ignores the conflict policy.
				p := build(t, bucket("A", "o", entry("/a", "local_1", 9, 1, "")), bucket("B", "o", entry("/b", "local_1", 5, 1, "")), o)
				if got := p.Ops[0].Action; got != srcNewer[m] {
					t.Errorf("SourceNewer: got %v want %v", got, srcNewer[m])
				}
				// DestNewer.
				want := Skip
				if c == PreferSource {
					want = srcNewer[m]
				}
				p = build(t, bucket("A", "o", entry("/a", "local_1", 5, 1, "")), bucket("B", "o", entry("/b", "local_1", 9, 1, "")), o)
				if got := p.Ops[0].Action; got != want {
					t.Errorf("DestNewer: got %v want %v", got, want)
				}
				if p.Ops[0].Class != DestNewer {
					t.Errorf("class %v", p.Ops[0].Class)
				}
				wantConflicts := 0
				if want == Skip {
					wantConflicts = 1
				}
				if p.Conflicts != wantConflicts {
					t.Errorf("conflicts %d want %d", p.Conflicts, wantConflicts)
				}
			})
		}
	}
}

func TestOverrides(t *testing.T) {
	mk := func(ov map[string]Action, mut func(*store.Entry)) Op {
		s := entry("/a", "local_1", 5, 1, "")
		if mut != nil {
			mut(s)
		}
		o := opts()
		o.Overrides = ov
		return build(t, bucket("A", "o", s), bucket("B", "o", entry("/b", "local_1", 9, 1, "")), o).Ops[0]
	}
	if op := mk(map[string]Action{"local_1": Merge}, nil); op.Action != Merge || op.Class != DestNewer {
		t.Errorf("override merge: %v %v", op.Action, op.Class)
	}
	if op := mk(map[string]Action{"local_1": Overwrite}, nil); op.Action != Overwrite {
		t.Errorf("override overwrite: %v", op.Action)
	}
	if op := mk(map[string]Action{"local_1": Add}, nil); op.Action != Merge {
		t.Errorf("Add with existing dest should become Merge, got %v", op.Action)
	}
	orphan := func(e *store.Entry) { e.TranscriptExists = false }
	if op := mk(map[string]Action{"local_1": Merge}, orphan); op.Action != Skip || op.Class != Orphan {
		t.Errorf("orphan override must be ignored: %v %v", op.Action, op.Class)
	}
	// Skip override on a normally applied op.
	p := build(t, bucket("A", "o", entry("/a", "local_9", 5, 1, "")), bucket("B", "o"), func() Options {
		o := opts()
		o.Overrides = map[string]Action{"local_9": Skip}
		return o
	}())
	if p.Ops[0].Action != Skip || p.Adds != 0 || p.Skips != 1 {
		t.Errorf("skip override: %+v", p)
	}
	// Add override on Missing stays Add; Merge on Missing becomes Add.
	o := opts()
	o.Overrides = map[string]Action{"local_9": Merge}
	p = build(t, bucket("A", "o", entry("/a", "local_9", 5, 1, "")), bucket("B", "o"), o)
	if p.Ops[0].Action != Add {
		t.Errorf("merge override on missing: %v", p.Ops[0].Action)
	}
}

func TestOrphans(t *testing.T) {
	s := entry("/a", "local_1", 5, 1, "")
	s.TranscriptExists = false
	o := opts()
	o.IncludeOrphans = true
	p := build(t, bucket("A", "o", s), bucket("B", "o"), o)
	op := p.Ops[0]
	if op.Action != Add || p.Orphans != 0 || !strings.Contains(op.Reason, "orphan") {
		t.Errorf("included orphan: %+v", op)
	}
	p = build(t, bucket("A", "o", s), bucket("B", "o"), opts())
	if p.Orphans != 1 || p.Skips != 1 || p.Ops[0].Action != Skip {
		t.Errorf("skipped orphan: %+v", p)
	}
}

func TestUnreadableDest(t *testing.T) {
	src := bucket("A", "o", entry("/a", "local_1", 5, 1, ""))
	dst := bucket("B", "o")
	dst.Unreadable = []store.UnreadableEntry{{Path: "/s/B/o/local_1.json"}}
	o := opts()
	o.Overrides = map[string]Action{"local_1": Overwrite}
	p := build(t, src, dst, o)
	if op := p.Ops[0]; op.Action != Skip || op.Reason != "destination file is unreadable" {
		t.Errorf("%+v", op)
	}
}

func TestCrossOrg(t *testing.T) {
	e := entry("/a", "local_1", 5, 1, "")
	if p := build(t, bucket("A", "o1", e), bucket("B", "o2"), opts()); !p.Ops[0].CrossOrg {
		t.Error("expected CrossOrg")
	}
	if p := build(t, bucket("A", "o1", e), bucket("B", "o1"), opts()); p.Ops[0].CrossOrg {
		t.Error("unexpected CrossOrg")
	}
}

func TestBuildErrors(t *testing.T) {
	e := entry("/a", "local_1", 5, 1, "")
	src := bucket("A", "o", e)
	if _, err := Build(src, src, src.Entries, opts()); err == nil {
		t.Error("same bucket accepted")
	}
	other := entry("/x", "local_2", 5, 1, "")
	if _, err := Build(src, bucket("B", "o"), []*store.Entry{other}, opts()); err == nil {
		t.Error("foreign entry accepted")
	}
}

func TestOrderAndSummary(t *testing.T) {
	a := entry("/a", "local_a", 5, 1, "")
	b := entry("/a", "local_b", 9, 1, "")
	c := entry("/a", "local_c", 5, 1, "")
	d := entry("/a", "local_d", 5, 1, "")
	d.TranscriptExists = false
	src := bucket("A", "o", a, b, c, d)
	dst := bucket("B", "o", entry("/b", "local_b", 5, 1, ""), entry("/b", "local_c", 9, 1, ""))
	p, err := Build(src, dst, []*store.Entry{d, c, b, a}, opts())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, op := range p.Ops {
		ids = append(ids, op.Source.SessionID)
	}
	if strings.Join(ids, ",") != "local_d,local_c,local_b,local_a" {
		t.Errorf("order %v", ids)
	}
	if want := "1 to add · 1 to merge · 1 kept (destination newer) · 1 orphans skipped"; p.Summary() != want {
		t.Errorf("summary %q", p.Summary())
	}
	if n := len(p.Changes()); n != 2 {
		t.Errorf("changes %d", n)
	}
	o := opts()
	o.Mode = ModeOverwrite
	p, _ = Build(src, dst, []*store.Entry{b}, o)
	if want := "0 to add · 0 to merge · 1 to overwrite · 0 kept (destination newer) · 0 orphans skipped"; p.Summary() != want {
		t.Errorf("summary %q", p.Summary())
	}
}

func TestDiffKeysDropDestOwned(t *testing.T) {
	src := entry("/a", "local_1", 9, 1, `,"remoteMcpServersConfig":["a"],"model":"a"`)
	dst := entry("/b", "local_1", 5, 1, `,"remoteMcpServersConfig":["b"],"model":"b"`)
	has := func(keys []string, k string) bool {
		for _, x := range keys {
			if x == k {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name     string
		mode     Mode
		conflict OnConflict
		src, dst *store.Entry
		wantDest bool // remoteMcpServersConfig still listed
	}{
		{"merge source newer", ModeMerge, KeepDest, src, dst, false},
		{"merge dest newer kept", ModeMerge, KeepDest, dst, src, false},
		{"merge dest newer prefer source", ModeMerge, PreferSource, dst, src, false},
		{"overwrite source newer", ModeOverwrite, KeepDest, src, dst, true},
		{"overwrite dest newer prefer source", ModeOverwrite, PreferSource, dst, src, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := opts()
			o.Mode, o.OnConflict = tc.mode, tc.conflict
			s, d := *tc.src, *tc.dst
			p := build(t, bucket("A", "o", &s), bucket("B", "o", &d), o)
			keys := p.Ops[0].DiffKeys
			if got := has(keys, "remoteMcpServersConfig"); got != tc.wantDest {
				t.Fatalf("remoteMcpServersConfig listed=%v, want %v (keys %v)", got, tc.wantDest, keys)
			}
			if !has(keys, "model") {
				t.Fatalf("source-owned key model missing from %v", keys)
			}
		})
	}
}

func TestDiffKeysOnlyWhatMergeChanges(t *testing.T) {
	src := entry("/a", "local_1", 9, 1, `,"model":"a","srcOnly":1,"both":"a"`)
	dst := entry("/b", "local_1", 5, 1, `,"model":"b","destOnlyKey":1,"both":"b"`)
	p := build(t, bucket("A", "o", src), bucket("B", "o", dst), opts())
	want := "lastActivityAt,model,srcOnly"
	if got := strings.Join(p.Ops[0].DiffKeys, ","); got != want {
		t.Fatalf("merge DiffKeys = %s, want %s", got, want)
	}
	o := opts()
	o.Mode = ModeOverwrite
	p = build(t, bucket("A", "o", src), bucket("B", "o", dst), o)
	if got := strings.Join(p.Ops[0].DiffKeys, ","); got != "both,destOnlyKey,lastActivityAt,model,srcOnly" {
		t.Fatalf("overwrite DiffKeys = %s", got)
	}
}

func TestAlsoInOtherOrgBucket(t *testing.T) {
	const org2 = "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	src := bucket("A", "o", entry("/a", "local_1", 5, 5, ""), entry("/a", "local_2", 5, 5, ""))
	dst := bucket("B", "o")
	other := bucket("B", org2, entry("/c", "local_1", 5, 5, ""))
	o := opts()
	o.DestAccountBuckets = []*store.Bucket{dst, other}
	p := build(t, src, dst, o)
	got := p.Ops[0]
	if got.Action != Add || got.Class != Missing || got.AlsoInOrg != org2 {
		t.Fatalf("op = %v/%v also=%q", got.Class, got.Action, got.AlsoInOrg)
	}
	if want := "not in this org yet; already in this account's 22222222… bucket"; got.Reason != want {
		t.Fatalf("reason %q, want %q", got.Reason, want)
	}
	if p.Ops[1].AlsoInOrg != "" || p.Ops[1].Reason != "not in the destination yet" {
		t.Fatalf("unrelated chat changed: %+v", p.Ops[1])
	}
	// Without the option the behaviour is unchanged.
	p = build(t, src, dst, opts())
	if p.Ops[0].AlsoInOrg != "" || p.Ops[0].Reason != "not in the destination yet" {
		t.Fatalf("nil buckets changed behaviour: %+v", p.Ops[0])
	}
}

func TestParsers(t *testing.T) {
	for s, want := range map[string]Mode{"merge": ModeMerge, "overwrite": ModeOverwrite, "add-only": ModeAddOnly, "": ModeMerge} {
		if got, err := ParseMode(s); err != nil || got != want {
			t.Errorf("ParseMode(%q)", s)
		}
	}
	for s, want := range map[string]OnConflict{"keep-dest": KeepDest, "prefer-source": PreferSource, "skip": SkipConflicts, "": KeepDest} {
		if got, err := ParseOnConflict(s); err != nil || got != want {
			t.Errorf("ParseOnConflict(%q)", s)
		}
	}
	if _, err := ParseMode("x"); err == nil {
		t.Error("bad mode accepted")
	}
	if _, err := ParseOnConflict("x"); err == nil {
		t.Error("bad policy accepted")
	}
	if ModeAddOnly.String() != "add-only" || SkipConflicts.String() != "skip" || MetaOnly.String() != "MetaOnly" || Overwrite.String() != "Overwrite" {
		t.Error("String()")
	}
}
