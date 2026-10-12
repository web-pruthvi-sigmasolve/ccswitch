package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

const (
	accA = "11111111-1111-4111-8111-111111111111"
	accB = "22222222-2222-4222-8222-222222222222"
	orgA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	orgB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

func noEnv(string) string { return "" }

func TestDiscoverLinuxChosenOverEmptyFlatpak(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	f.AddEntry(accA, orgA, testutil.Entry{SessionID: "local_" + accA[:8], CLISessionID: "c1", Title: "one", Cwd: "/p/one", LastActivityAt: 1000})
	f.AddEntry(accA, orgA, testutil.Entry{SessionID: "local_two", CLISessionID: "c2", Title: "two", Cwd: "/p/two", LastActivityAt: 3000})
	f.AddEntry(accB, orgB, testutil.Entry{SessionID: "local_three", CLISessionID: "c3", Title: "three", Cwd: "/p/three", LastActivityAt: 2000})
	f.AddBucket(accB, orgA) // empty bucket stays a bucket
	f.AddTranscript("/p/two", "c2")
	flat := filepath.Join(f.Home, ".var", "app", "com.anthropic.Claude", "config", "Claude")
	if err := os.MkdirAll(flat, 0o755); err != nil {
		t.Fatal(err)
	}

	st, err := Discover(Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if st.UserData != f.UserData {
		t.Fatalf("UserData = %q, want %q", st.UserData, f.UserData)
	}
	if st.TranscriptRoot != f.TranscriptRoot {
		t.Fatalf("TranscriptRoot = %q", st.TranscriptRoot)
	}
	if len(st.Candidates) != 2 {
		t.Fatalf("candidates = %+v", st.Candidates)
	}
	var chosen, other *Candidate
	for i := range st.Candidates {
		if st.Candidates[i].Chosen {
			chosen = &st.Candidates[i]
		} else {
			other = &st.Candidates[i]
		}
	}
	if chosen == nil || other == nil {
		t.Fatalf("candidates = %+v", st.Candidates)
	}
	if chosen.Entries != 3 || chosen.Buckets != 2 || chosen.Reason != "chosen: 3 chats in 2 buckets" {
		t.Errorf("chosen = %+v", *chosen)
	}
	if other.Reason != "no claude-code-sessions folder" {
		t.Errorf("other reason = %q", other.Reason)
	}
	if len(st.Accounts) != 2 {
		t.Fatalf("accounts = %d", len(st.Accounts))
	}
	// accA has the newest activity (3000).
	if st.Accounts[0].UUID != accA || st.Accounts[1].UUID != accB {
		t.Errorf("account order = %s, %s", st.Accounts[0].UUID, st.Accounts[1].UUID)
	}
	a := st.Accounts[0]
	if len(a.Buckets) != 1 || len(a.Buckets[0].Entries) != 2 || a.Buckets[0].Entries[0].SessionID != "local_two" {
		t.Errorf("accA entries not newest-first")
	}
	if !a.Buckets[0].Entries[0].TranscriptExists {
		t.Errorf("transcript for local_two not found")
	}
	b := st.Account(accB)
	if len(b.Buckets) != 2 {
		t.Fatalf("accB buckets = %d, want 2 (including empty)", len(b.Buckets))
	}
	if b.Buckets[0].OrgUUID != orgB || len(b.Buckets[1].Entries) != 0 {
		t.Errorf("accB bucket order/empty bucket wrong")
	}
}

func TestDiscoverChoosesMostEntries(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	f.AddEntry(accA, orgA, testutil.Entry{SessionID: "local_a", LastActivityAt: 5000})
	// second candidate via XDG_CONFIG_HOME with more entries
	xdg := filepath.Join(f.Home, "xdg")
	for _, n := range []string{"local_x", "local_y"} {
		p := filepath.Join(xdg, "Claude", "claude-code-sessions", accB, orgB, n+".json")
		f.WriteFile(p, []byte(`{"sessionId":"`+n+`"}`))
	}
	env := func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdg
		}
		return ""
	}
	st, err := Discover(Options{HomeDir: f.Home, Getenv: env, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if st.UserData != filepath.Join(xdg, "Claude") {
		t.Fatalf("UserData = %q", st.UserData)
	}
	for _, c := range st.Candidates {
		if !c.Chosen && !strings.Contains(c.Reason, "fewer chats") {
			t.Errorf("reason = %q", c.Reason)
		}
	}
}

func TestDiscoverOverrideWins(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	f.AddEntry(accA, orgA, testutil.Entry{SessionID: "local_a", LastActivityAt: 5000})
	other := t.TempDir()
	p := filepath.Join(other, "claude-code-sessions", accB, orgB, "local_o.json")
	f.WriteFile(p, []byte(`{"sessionId":"local_o"}`))

	st, err := Discover(Options{UserDataOverride: other, HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if st.UserData != other || len(st.Candidates) != 1 || len(st.Accounts) != 1 {
		t.Fatalf("store = %+v", st)
	}
}

func TestDiscoverNotFound(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	empty := filepath.Join(t.TempDir(), "nothing")
	tests := []struct {
		name string
		opts Options
	}{
		{"override", Options{UserDataOverride: empty, HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"}},
		{"auto", Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"}},
		{"darwin", Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "darwin"}},
		{"windows", Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "windows"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, err := Discover(tc.opts)
			if !errors.Is(err, ErrStoreNotFound) {
				t.Fatalf("err = %v", err)
			}
			if st == nil || st.Root == "" || len(st.Candidates) == 0 {
				t.Fatalf("store = %+v", st)
			}
			if !strings.Contains(err.Error(), "claude-code-sessions") {
				t.Errorf("error should list checked paths: %v", err)
			}
		})
	}
}

func TestDiscoverScanFallback(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	odd := filepath.Join(f.Home, ".config", "SomeFork", "data")
	f.WriteFile(filepath.Join(odd, "claude-code-sessions", accA, orgA, "local_s.json"), []byte(`{"sessionId":"local_s"}`))
	st, err := Discover(Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if st.UserData != odd {
		t.Fatalf("UserData = %q, want %q", st.UserData, odd)
	}
}

func TestDiscoverWindowsAndMac(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	roaming := filepath.Join(f.Home, "AppData", "Roaming")
	f.WriteFile(filepath.Join(roaming, "Claude", "claude-code-sessions", accA, orgA, "local_w.json"), []byte(`{"sessionId":"local_w"}`))
	env := func(k string) string {
		switch k {
		case "APPDATA":
			return roaming
		case "LOCALAPPDATA":
			return filepath.Join(f.Home, "AppData", "Local")
		}
		return ""
	}
	st, err := Discover(Options{HomeDir: f.Home, Getenv: env, GOOS: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	if st.UserData != filepath.Join(roaming, "Claude") {
		t.Errorf("windows UserData = %q", st.UserData)
	}

	mac := filepath.Join(f.Home, "Library", "Containers", "x.y", "Data", "Library", "Application Support", "Claude")
	f.WriteFile(filepath.Join(mac, "claude-code-sessions", accA, orgA, "local_m.json"), []byte(`{"sessionId":"local_m"}`))
	st, err = Discover(Options{HomeDir: f.Home, Getenv: noEnv, GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	if st.UserData != mac {
		t.Errorf("darwin UserData = %q", st.UserData)
	}
}

func TestTranscriptRootFromEnv(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	f.AddEntry(accA, orgA, testutil.Entry{SessionID: "local_a", LastActivityAt: 5000})
	env := func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return "/custom/cc"
		}
		return ""
	}
	st, err := Discover(Options{HomeDir: f.Home, Getenv: env, GOOS: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if st.TranscriptRoot != filepath.Join("/custom/cc", "projects") {
		t.Errorf("TranscriptRoot = %q", st.TranscriptRoot)
	}
}

func TestIsUUID(t *testing.T) {
	tests := map[string]bool{
		accA:                                   true,
		"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA": false,
		"not-a-uuid":                           false,
		"":                                     false,
	}
	for in, want := range tests {
		if IsUUID(in) != want {
			t.Errorf("IsUUID(%q) != %v", in, want)
		}
	}
}
