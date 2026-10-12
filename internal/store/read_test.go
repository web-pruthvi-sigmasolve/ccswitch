package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ccswitch/ccswitch/internal/testutil"
)

func TestReadEntryFixture(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	p := f.AddEntry(accA, orgA, testutil.Entry{
		SessionID: "local_abc", CLISessionID: "cli-1", Title: "Hello", Cwd: "/var/www/X",
		LastActivityAt: 2000, LastFocusedAt: 3000, IsArchived: true,
		Extra: map[string]any{"futureKey": map[string]any{"a": 1}},
	})
	e, err := ReadEntry(p)
	if err != nil {
		t.Fatal(err)
	}
	if e.SessionID != "local_abc" || e.CLISessionID != "cli-1" || e.Title != "Hello" ||
		e.Cwd != "/var/www/X" || e.OriginCwd != "/var/www/X" || e.LastActivityAt != 2000 ||
		e.LastFocusedAt != 3000 || e.CreatedAt != 2000 || !e.IsArchived {
		t.Errorf("entry = %+v", e)
	}
	raw, _ := os.ReadFile(p)
	if string(e.Raw) != string(raw) || len(raw) == 0 || raw[len(raw)-1] == '\n' {
		t.Errorf("raw mismatch or trailing newline")
	}
	if e.Mode.Perm() != 0o600 || e.ModTime.IsZero() {
		t.Errorf("mode/mtime = %v %v", e.Mode, e.ModTime)
	}
	if e.Raw[0] != '{' {
		t.Errorf("not single-line JSON")
	}
}

func TestReadEntryVariants(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		wantErr bool
		title   string
	}{
		{"bom+crlf", "local_x", "\xEF\xBB\xBF{\r\n\"sessionId\":\"local_x\",\r\n\"title\":\"T\"\r\n}\r\n", false, "T"},
		{"plain", "local_x", `{"sessionId":"local_x","title":"P"}`, false, "P"},
		{"float ms", "local_x", `{"sessionId":"local_x","lastActivityAt":1.5e3}`, false, ""},
		{"mismatch", "local_x", `{"sessionId":"local_y"}`, true, ""},
		{"empty id", "local_x", `{"title":"no id"}`, true, ""},
		{"bad json", "local_x", `{"sessionId":`, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), tc.file+".json")
			if err := os.WriteFile(p, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			e, err := ReadEntry(p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err == nil {
				if e.Title != tc.title {
					t.Errorf("title = %q", e.Title)
				}
				if string(e.Raw) != tc.content {
					t.Errorf("Raw not exact")
				}
			}
		})
	}
	if _, err := ReadEntry(filepath.Join(t.TempDir(), "local_none.json")); err == nil {
		t.Error("missing file should error")
	}
}

func TestReadBucketUnreadableAndOrder(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	dir := f.AddBucket(accA, orgA)
	f.AddEntry(accA, orgA, testutil.Entry{SessionID: "local_old", LastActivityAt: 100})
	f.AddEntry(accA, orgA, testutil.Entry{SessionID: "local_new", LastActivityAt: 900})
	f.WriteFile(filepath.Join(dir, "local_bad.json"), []byte(`{"sessionId":"local_other"}`))
	f.WriteFile(filepath.Join(dir, "local_junk.json"), []byte(`garbage`))
	f.WriteFile(filepath.Join(dir, "scheduled-tasks.json"), []byte(`{}`))

	b := &Bucket{AccountUUID: accA, OrgUUID: orgA, Path: dir}
	ReadBucket(b, f.TranscriptRoot)
	if len(b.Entries) != 2 || b.Entries[0].SessionID != "local_new" {
		t.Errorf("entries = %v", b.Entries)
	}
	if len(b.Unreadable) != 2 {
		t.Fatalf("unreadable = %d", len(b.Unreadable))
	}
	var mismatch bool
	for _, u := range b.Unreadable {
		if errors.Is(u.Err, errSessionIDMismatch) {
			mismatch = true
		}
	}
	if !mismatch {
		t.Error("expected a sessionId mismatch error")
	}
}

func TestReadBucketMany(t *testing.T) {
	f := testutil.NewFixtureStore(t)
	for i := 0; i < 200; i++ {
		id := "local_" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('a'+i/676))
		f.AddEntry(accA, orgA, testutil.Entry{SessionID: id, LastActivityAt: int64(1000 + i)})
	}
	b := &Bucket{AccountUUID: accA, OrgUUID: orgA, Path: filepath.Join(f.UserData, "claude-code-sessions", accA, orgA)}
	ReadBucket(b, "")
	if len(b.Entries) != 200 || len(b.Unreadable) != 0 {
		t.Fatalf("entries=%d unreadable=%d", len(b.Entries), len(b.Unreadable))
	}
	for i := 1; i < len(b.Entries); i++ {
		if b.Entries[i-1].Activity() < b.Entries[i].Activity() {
			t.Fatal("not sorted")
		}
	}
}
