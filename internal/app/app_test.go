package app

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

const (
	accA = "aaaaaaaa-1111-4111-8111-111111111111"
	accB = "aaaaaa22-2222-4222-8222-222222222222" // shares the "aaaaaa" prefix with accA
	accC = "cccccccc-3333-4333-8333-333333333333"
	org1 = "11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	org2 = "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

var now = time.UnixMilli(1_700_000_000_000)

func ms(daysAgo float64) int64 {
	return now.Add(-time.Duration(daysAgo * 24 * float64(time.Hour))).UnixMilli()
}

func newApp(t *testing.T) (*App, *testutil.Fixture) {
	t.Helper()
	fx := testutil.NewFixtureStore(t)
	fx.AddEntry(accA, org1, testutil.Entry{SessionID: "local_aaaa0001-0000-4000-8000-000000000001", CLISessionID: "c1", Title: "Fix pint failures", Cwd: "/work/shop", LastActivityAt: ms(1)})
	fx.AddEntry(accA, org1, testutil.Entry{SessionID: "local_aaaa0002-0000-4000-8000-000000000002", CLISessionID: "c2", Title: "Add webhook retries", Cwd: "/work/api", LastActivityAt: ms(10)})
	fx.AddEntry(accA, org1, testutil.Entry{SessionID: "local_bbbb0003-0000-4000-8000-000000000003", CLISessionID: "c3", Title: "Old archived", Cwd: "/work/shop", LastActivityAt: ms(20), IsArchived: true})
	fx.AddEntry(accA, org2, testutil.Entry{SessionID: "local_cccc0004-0000-4000-8000-000000000004", CLISessionID: "c4", Title: "Other org", Cwd: "/other", LastActivityAt: ms(30)})
	fx.AddEntry(accB, org1, testutil.Entry{SessionID: "local_dddd0005-0000-4000-8000-000000000005", CLISessionID: "c5", Title: "Personal", Cwd: "/home/me/notes", LastActivityAt: ms(2)})
	fx.AddAccount(accC)
	cfgDir := t.TempDir()
	cfg := &config.Config{Labels: map[string]string{accA: "Work", accB: "personal"}, BackupRetention: 20}
	if err := cfg.Save(cfgDir); err != nil {
		t.Fatal(err)
	}
	a, err := Load(LoadOptions{UserData: fx.UserData, ConfigDir: cfgDir})
	if err != nil {
		t.Fatal(err)
	}
	if a.StoreErr != nil {
		t.Fatal(a.StoreErr)
	}
	return a, fx
}

func TestResolveAccount(t *testing.T) {
	a, _ := newApp(t)
	// Store order: A (ms(1)) newest, then B (ms(2)), C has no chats.
	if got := a.Store.Accounts[0].UUID; got != accA {
		t.Fatalf("unexpected order, first = %s", got)
	}
	tests := []struct {
		ref, want, errSub string
	}{
		{"1", accA, ""},
		{"2", accB, ""},
		{"3", accC, ""},
		{"4", "", "no account number 4"},
		{"0", "", "no account number 0"},
		{accC, accC, ""},
		{strings.ToUpper(accC), accC, ""},
		{"cccccc", accC, ""},
		{"aaaaaa", "", "more than one"},
		{"aaaaaaaa-1", accA, ""},
		{"work", accA, ""},
		{"WORK", accA, ""},
		{"Personal", accB, ""},
		{"nobody", "", "No account matches"},
		{"ccc", "", "No account matches"}, // too short for a prefix
		{"", "", "No account was given"},
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			acc, err := a.ResolveAccount(tc.ref)
			if tc.errSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errSub) {
					t.Fatalf("want error containing %q, got %v", tc.errSub, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if acc.UUID != tc.want {
				t.Fatalf("got %s want %s", acc.UUID, tc.want)
			}
		})
	}
}

func TestResolveAccountAmbiguousLabel(t *testing.T) {
	a, _ := newApp(t)
	a.Config.SetLabel(accB, "work")
	_, err := a.ResolveAccount("work")
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("want ambiguous error, got %v", err)
	}
	var he *HintError
	if !errors.As(err, &he) || he.Hint == "" {
		t.Fatalf("want HintError with hint, got %#v", err)
	}
}

func TestResolveAccountStoreMissing(t *testing.T) {
	a, err := Load(LoadOptions{UserData: t.TempDir(), ConfigDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if a.StoreErr == nil {
		t.Fatal("want StoreErr")
	}
	if _, err := a.ResolveAccount("1"); err == nil {
		t.Fatal("want error")
	}
}

func TestBuckets(t *testing.T) {
	a, _ := newApp(t)
	accAcc, _ := a.ResolveAccount("1")
	if b := a.PrimaryBucket(accAcc); b == nil || b.OrgUUID != org1 {
		t.Fatalf("primary bucket = %+v", b)
	}
	empty, _ := a.ResolveAccount(accC)
	if a.PrimaryBucket(empty) != nil {
		t.Fatal("empty account must have no primary bucket")
	}
	if _, err := a.BucketFor(empty, ""); err == nil {
		t.Fatal("want error for empty account without org")
	}
	b, err := a.BucketFor(empty, org2)
	if err != nil || b.OrgUUID != org2 || !strings.HasSuffix(b.Path, org2) || !strings.HasPrefix(b.Path, empty.Path) {
		t.Fatalf("synthetic bucket = %+v, %v", b, err)
	}
	if b, err := a.BucketFor(accAcc, org2); err != nil || len(b.Entries) != 1 {
		t.Fatalf("explicit org: %+v %v", b, err)
	}
	if _, err := a.BucketFor(accAcc, "33333333-cccc-4ccc-8ccc-cccccccccccc"); err == nil {
		t.Fatal("want error for unknown org on a non-empty account")
	}
	src := accAcc.Bucket(org2)
	accBAcc, _ := a.ResolveAccount(accB)
	// B has no org2 bucket -> primary (org1).
	if b, err := a.DestBucket(accBAcc, src, ""); err != nil || b.OrgUUID != org1 {
		t.Fatalf("dest fallback = %+v %v", b, err)
	}
	if b, err := a.DestBucket(empty, src, ""); err != nil || b.OrgUUID != org2 {
		t.Fatalf("dest synthetic = %+v %v", b, err)
	}
}

func TestSelect(t *testing.T) {
	a, _ := newApp(t)
	acc, _ := a.ResolveAccount("1")
	b := acc.Bucket(org1)

	tests := []struct {
		name    string
		f       Filter
		want    []string // titles
		errIs   error
		wantErr bool
	}{
		{name: "all skip archived", f: Filter{All: true, SkipArchived: true}, want: []string{"Fix pint failures", "Add webhook retries"}},
		{name: "all includes archived by default", f: Filter{All: true}, want: []string{"Fix pint failures", "Add webhook retries", "Old archived"}},
		{name: "project name", f: Filter{Projects: []string{"SHOP"}}, want: []string{"Fix pint failures", "Old archived"}},
		{name: "project skip archived", f: Filter{Projects: []string{"shop"}, SkipArchived: true}, want: []string{"Fix pint failures"}},
		{name: "since includes archived", f: Filter{Since: 365 * 24 * time.Hour, All: true}, want: []string{"Fix pint failures", "Add webhook retries", "Old archived"}},
		{name: "project suffix", f: Filter{Projects: []string{"work/api"}}, want: []string{"Add webhook retries"}},
		{name: "project archived too", f: Filter{Projects: []string{"shop"}}, want: []string{"Fix pint failures", "Old archived"}},
		{name: "since", f: Filter{Since: 7 * 24 * time.Hour, All: true}, want: []string{"Fix pint failures"}},
		{name: "title", f: Filter{TitleMatch: "WEBHOOK"}, want: []string{"Add webhook retries"}},
		{name: "id full", f: Filter{IDs: []string{"local_aaaa0001-0000-4000-8000-000000000001"}}, want: []string{"Fix pint failures"}},
		{name: "id bare uuid", f: Filter{IDs: []string{"aaaa0002-0000-4000-8000-000000000002"}}, want: []string{"Add webhook retries"}},
		{name: "id prefix", f: Filter{IDs: []string{"aaaa0002"}}, want: []string{"Add webhook retries"}},
		{name: "id archived allowed", f: Filter{IDs: []string{"bbbb0003"}}, want: []string{"Old archived"}},
		{name: "id ambiguous", f: Filter{IDs: []string{"aaaa"}}, wantErr: true},
		{name: "id unknown", f: Filter{IDs: []string{"zzzz9999"}}, errIs: ErrNoMatch},
		{name: "no match", f: Filter{TitleMatch: "nothing like this"}, errIs: ErrNoMatch},
		{name: "no filter", f: Filter{}, errIs: ErrNoFilter},
		{name: "and combination", f: Filter{Projects: []string{"shop"}, TitleMatch: "webhook"}, errIs: ErrNoMatch},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Select(b, tc.f, now)
			if tc.errIs != nil || tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %d entries", len(got))
				}
				if tc.errIs != nil && !errors.Is(err, tc.errIs) {
					t.Fatalf("want %v, got %v", tc.errIs, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var gt []string
			for _, e := range got {
				gt = append(gt, e.Title)
			}
			if strings.Join(gt, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("got %v want %v", gt, tc.want)
			}
		})
	}
}

func TestSelectAllOnEmptyBucket(t *testing.T) {
	a, _ := newApp(t)
	acc, _ := a.ResolveAccount(accB)
	got, err := Select(acc.Buckets[0], Filter{All: true, Since: time.Hour}, now)
	if err == nil || !errors.Is(err, ErrNoMatch) {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestParseDuration(t *testing.T) {
	ok := map[string]time.Duration{
		"7d":    7 * 24 * time.Hour,
		"2w":    14 * 24 * time.Hour,
		"24h":   24 * time.Hour,
		"30m":   30 * time.Minute,
		"1h30m": 90 * time.Minute,
		" 3D ":  72 * time.Hour,
	}
	for in, want := range ok {
		got, err := ParseDuration(in)
		if err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "0d", "-5h", "7x", "d"} {
		if _, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) should fail", in)
		}
	}
}

func TestLock(t *testing.T) {
	a, _ := newApp(t)
	release, err := a.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Lock(); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("second lock must fail, got %v", err)
	} else {
		var he *HintError
		if !errors.As(err, &he) || !strings.Contains(he.Hint, config.LockPath(a.ConfigDir)) {
			t.Fatalf("hint must name the lock path: %#v", err)
		}
	}
	release()
	release() // idempotent
	if _, err := os.Stat(config.LockPath(a.ConfigDir)); !os.IsNotExist(err) {
		t.Fatalf("lock file should be gone: %v", err)
	}
	r2, err := a.Lock()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	r2()
}

func TestLockStalePid(t *testing.T) {
	a, _ := newApp(t)
	p := config.LockPath(a.ConfigDir)
	if err := os.WriteFile(p, []byte("2147483646\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, err := a.Lock()
	if err != nil {
		t.Fatalf("stale lock should be replaced: %v", err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), strconv.Itoa(os.Getpid())) {
		t.Fatalf("lock should hold our pid, got %q", b)
	}
	release()
}

func TestLockOldFileReplaced(t *testing.T) {
	a, _ := newApp(t)
	p := config.LockPath(a.ConfigDir)
	if err := os.WriteFile(p, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := a.Lock()
	if err != nil {
		t.Fatalf("old lock should be replaced: %v", err)
	}
	release()
}

func TestLegacyLabelMigration(t *testing.T) {
	fx := testutil.NewFixtureStore(t)
	fx.AddEntry(accA, org1, testutil.Entry{SessionID: "local_aaaa0001-0000-4000-8000-000000000001", CLISessionID: "c1", Title: "x", Cwd: "/x", LastActivityAt: ms(1)})
	cfgDir := t.TempDir()
	if err := os.WriteFile(cfgDir+"/accounts.conf", []byte("# old\n"+accA+"/"+org1+"=legacy name\nnot-a-uuid=skip\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := Load(LoadOptions{UserData: fx.UserData, ConfigDir: cfgDir})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.Config.Labels[accA]; got != "legacy name" {
		t.Fatalf("label = %q", got)
	}
	if len(a.Config.Labels) != 1 {
		t.Fatalf("labels = %v", a.Config.Labels)
	}
}
