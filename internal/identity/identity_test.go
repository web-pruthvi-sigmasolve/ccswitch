package identity

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

const (
	accA = "0688fb6b-1111-4111-8111-111111111111"
	accB = "22222222-2222-4222-8222-222222222222"
	accC = "33333333-3333-4333-8333-333333333333"
	accD = "44444444-4444-4444-8444-444444444444"
	org  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

func noEnv(string) string { return "" }

// v8 mimics V8 structured-clone strings: a quote tag, a length byte, the text.
func v8(s string) string { return "\"" + string(rune(len(s))) + s }

func setup(t *testing.T) (*testutil.Fixture, *store.Store) {
	t.Helper()
	f := testutil.NewFixtureStore(t)
	f.AddEntry(accA, org, testutil.Entry{SessionID: "local_a1", Title: "alpha", Cwd: "/w/shop", LastActivityAt: 5000})
	f.AddEntry(accB, org, testutil.Entry{SessionID: "local_b1", Title: "beta", Cwd: "/w/shop", LastActivityAt: 4000})
	f.AddEntry(accC, org, testutil.Entry{SessionID: "local_c1", Title: "gamma", Cwd: "/w/shop", LastActivityAt: 3000})
	f.AddEntry(accD, org, testutil.Entry{SessionID: "local_d1", Title: "delta", Cwd: "/w/shop", LastActivityAt: 2000})
	f.WriteFile(filepath.Join(f.UserData, "config.json"),
		[]byte(`{"oauth:tokenCache":"SECRET","lastKnownAccountUuid":"`+accA+`","theme":"dark"}`))
	f.WriteFile(filepath.Join(f.UserData, "logs", "main.log"),
		[]byte("2026 [account] Identity changed to "+accD+"\nnoise\n2026 [account] Identity changed from "+accD+" to "+accB+"\nmore noise\n"))
	idb := "\x00\x01junk" + v8("uuid") + accC + v8("email_address") + v8("carol@example.com") + v8("full_name") + v8("Carol Q") + v8("display_name") + v8("Carol")
	f.WriteFile(filepath.Join(f.UserData, "IndexedDB", "https_claude.ai_0.indexeddb.leveldb", "000003.log"), []byte(idb))
	st, err := store.Discover(store.Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	return f, st
}

func TestResolver(t *testing.T) {
	f, st := setup(t)
	cfg := &config.Config{Labels: map[string]string{accA: "work"}}
	r := New(st, cfg).WithPlatform("linux", f.Home)

	tests := []struct {
		uuid    string
		want    Label
		display string
		current bool
	}{
		{accA, Label{Name: "work", Source: "user"}, "work (current)", true},
		{accB, Label{Source: "last-signed-in"}, "account 22222222… (last signed in)", false},
		{accC, Label{Name: "Carol Q", Email: "carol@example.com", Source: "profile"}, "Carol Q <carol@example.com>", false},
		{accD, Label{}, "account 44444444…", false},
	}
	for _, tc := range tests {
		t.Run(tc.uuid[:8], func(t *testing.T) {
			if got := r.Label(tc.uuid); got != tc.want {
				t.Errorf("Label = %+v, want %+v", got, tc.want)
			}
			if got := r.Display(st.Account(tc.uuid)); got != tc.display {
				t.Errorf("Display = %q, want %q", got, tc.display)
			}
			if r.IsCurrent(tc.uuid) != tc.current {
				t.Errorf("IsCurrent = %v", !tc.current)
			}
		})
	}
}

func TestResolverMissingSources(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	f.AddEntry(accA, org, testutil.Entry{SessionID: "local_a1", LastActivityAt: 1})
	st, err := store.Discover(store.Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	r := New(st, nil)
	if got := r.Display(st.Accounts[0]); got != "account 0688fb6b…" {
		t.Errorf("Display = %q", got)
	}
}

func TestResolverMacLogLocation(t *testing.T) {
	f, st := setup(t)
	f.WriteFile(filepath.Join(f.Home, "Library", "Logs", "Claude", "main.log"),
		[]byte("[account] Identity changed to "+accC+"\n"))
	r := New(st, nil).WithPlatform("darwin", f.Home)
	if got := r.Display(st.Account(accC)); got != "Carol Q <carol@example.com> (last signed in)" {
		t.Errorf("Display = %q", got)
	}
	if got := r.Label(accC).Source; got != "last-signed-in" {
		t.Errorf("source = %q", got)
	}
}

func TestScanProfilesVariants(t *testing.T) {
	tests := []struct {
		name string
		text string
		want profile
		ok   bool
	}{
		{"name and email", accB + v8("email_address") + v8("me@x.com") + v8("full_name") + v8("Me X") + v8("end"), profile{"Me X", "me@x.com"}, true},
		{"display only", accB + v8("email_address") + v8("me@x.com") + v8("display_name") + v8("Disp") + v8("end"), profile{"Disp", "me@x.com"}, true},
		{"email only", accB + v8("email_address") + v8("me@x.com") + v8("tail"), profile{"", "me@x.com"}, true},
		{"no email", accB + v8("full_name") + v8("Me"), profile{}, false},
		{"email too far", accB + string(make([]byte, 2000)) + v8("email_address") + v8("me@x.com"), profile{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := map[string]profile{}
			scanProfiles(tc.text, out)
			got, ok := out[accB]
			if ok != tc.ok || got != tc.want {
				t.Errorf("got %+v ok=%v, want %+v ok=%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestFingerprint(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	add := func(id, title, cwd string, at int64) {
		f.AddEntry(accA, org, testutil.Entry{SessionID: id, Title: title, Cwd: cwd, LastActivityAt: at})
	}
	add("local_1", "one", "/w/shop", 1000)
	add("local_2", "", "/w/shop", 5000)
	add("local_3", "three", "/w/api", 3000)
	add("local_4", "four", "/w/api", 4000)
	add("local_5", "five", "/w/shop", 2000)
	add("local_6", "six", "/w/web", 500)
	add("local_7", "seven", "/w/docs", 400)
	st, err := store.Discover(store.Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	fp := FingerprintOf(st.Account(accA))
	if fp.Chats != 7 || !fp.LastActive.Equal(time.UnixMilli(5000)) {
		t.Errorf("fp = %+v", fp)
	}
	wantP := []string{"shop", "api", "docs"}
	for i, w := range wantP {
		if fp.TopProjects[i] != w {
			t.Errorf("TopProjects = %v, want %v", fp.TopProjects, wantP)
			break
		}
	}
	wantT := []string{"four", "three", "five"}
	if len(fp.RecentTitles) != 3 || fp.RecentTitles[0] != wantT[0] || fp.RecentTitles[1] != wantT[1] || fp.RecentTitles[2] != wantT[2] {
		t.Errorf("RecentTitles = %v, want %v", fp.RecentTitles, wantT)
	}
}
