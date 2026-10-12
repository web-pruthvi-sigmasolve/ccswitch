package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

func TestEncodeCwd(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/var/www/X", "-var-www-X"},
		{`C:\Users\me\x`, "C--Users-me-x"},
		{"/home/me/my project", "-home-me-my-project"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := EncodeCwd(tc.in); got != tc.want {
			t.Errorf("EncodeCwd(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLocateTranscript(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	cwdDir := t.TempDir()
	direct := f.AddTranscript(cwdDir, "cli-direct")
	// Stored under an unrelated folder name: only the glob fallback finds it.
	globbed := filepath.Join(f.TranscriptRoot, "weird-folder", "cli-glob.jsonl")
	f.WriteFile(globbed, []byte("{}\n"))
	// Dotted path: found via the looser encoding.
	f.AddTranscript("/x/y", "cli-ignored")
	loose := filepath.Join(f.TranscriptRoot, "-x-my-app", "cli-loose.jsonl")
	f.WriteFile(loose, []byte("{}\n"))

	tests := []struct {
		name       string
		e          Entry
		wantPath   string
		wantExists bool
		wantCwd    bool
	}{
		{"direct", Entry{CLISessionID: "cli-direct", Cwd: cwdDir}, direct, true, true},
		{"origin cwd preferred", Entry{CLISessionID: "cli-direct", Cwd: "/elsewhere", OriginCwd: cwdDir}, direct, true, true},
		{"cwd fallback", Entry{CLISessionID: "cli-direct", Cwd: cwdDir, OriginCwd: "/gone"}, direct, true, false},
		{"glob", Entry{CLISessionID: "cli-glob", Cwd: "/nowhere"}, globbed, true, false},
		{"loose", Entry{CLISessionID: "cli-loose", Cwd: "/x/my.app"}, loose, true, false},
		{"missing", Entry{CLISessionID: "cli-none", Cwd: "/nowhere"}, filepath.Join(f.TranscriptRoot, "-nowhere", "cli-none.jsonl"), false, false},
		{"no id", Entry{Cwd: "/nowhere"}, "", false, false},
		{"traversal id", Entry{CLISessionID: "../x", Cwd: "/nowhere"}, "", false, false},
		{"windows cwd", Entry{CLISessionID: "cli-win", Cwd: `C:\Users\me\x`}, filepath.Join(f.TranscriptRoot, "C--Users-me-x", "cli-win.jsonl"), true, false},
	}
	f.WriteFile(filepath.Join(f.TranscriptRoot, "C--Users-me-x", "cli-win.jsonl"), []byte("{}\n"))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.e
			p, ok := LocateTranscript(f.TranscriptRoot, &e)
			if p != tc.wantPath || ok != tc.wantExists || e.CwdExists != tc.wantCwd {
				t.Errorf("got (%q,%v,cwd=%v), want (%q,%v,cwd=%v)", p, ok, e.CwdExists, tc.wantPath, tc.wantExists, tc.wantCwd)
			}
		})
	}
	if _, err := os.Stat(direct); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopProcessName(t *testing.T) {
	for n, want := range map[string]bool{"claude": true, "Claude": true, "claude-desktop": true, "bash": false, "": false} {
		if isDesktopProcessName(n) != want {
			t.Errorf("isDesktopProcessName(%q) != %v", n, want)
		}
	}
	_ = ClaudeDesktopRunning() // must not panic or hang
}
