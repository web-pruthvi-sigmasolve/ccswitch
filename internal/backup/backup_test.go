package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRoundtripAndRestore(t *testing.T) {
	root := t.TempDir()
	orig := filepath.Join(t.TempDir(), "local_1.json")
	write(t, orig, `{"v":1}`)

	s, err := Begin(root, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Add(orig, []byte(`{"v":1}`), "acct", "org", "local_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(orig, []byte(`x`), "acct", "org", "local_1"); err == nil {
		t.Fatal("duplicate add accepted")
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(s.Dir, "acct", "org", "local_1.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup file: %v %v", st, err)
	}

	got, err := Latest(root)
	if err != nil || got.ID != s.ID || got.Note != "test" || len(got.Files) != 1 || got.Files[0].SessionID != "local_1" || got.Files[0].Size != 7 {
		t.Fatalf("latest: %+v %v", got, err)
	}

	write(t, orig, `{"v":2}`)
	if r, err := Restore(got, true); err != nil || len(r) != 1 {
		t.Fatal(r, err)
	}
	if b, _ := os.ReadFile(orig); string(b) != `{"v":2}` {
		t.Fatal("dry-run wrote")
	}
	if _, err := Restore(got, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(orig); string(b) != `{"v":1}` {
		t.Fatalf("not restored: %s", b)
	}
}

func TestRestoreDetectsCorruption(t *testing.T) {
	root := t.TempDir()
	orig := filepath.Join(t.TempDir(), "local_1.json")
	write(t, orig, `new`)
	s, _ := Begin(root, "")
	if err := s.Add(orig, []byte(`old`), "a", "o", "local_1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(s.Dir, "a", "o", "local_1.json"), "tampered")
	if _, err := Restore(s, false); err == nil || !strings.Contains(err.Error(), "corrupted") {
		t.Fatalf("err %v", err)
	}
	if b, _ := os.ReadFile(orig); string(b) != "new" {
		t.Fatal("original modified despite corrupt backup")
	}
}

func TestListOrderPruneDiscard(t *testing.T) {
	root := t.TempDir()
	var ids []string
	for i := 0; i < 4; i++ {
		s, err := Begin(root, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Add("/x/local_1.json", []byte("d"), "a", "o", "local_1"); err != nil {
			t.Fatal(err)
		}
		if err := s.Commit(); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID)
	}
	sets, err := List(root)
	if err != nil || len(sets) != 4 {
		t.Fatal(len(sets), err)
	}
	for i, s := range sets {
		if s.ID != ids[3-i] {
			t.Fatalf("order: %d %s want %s", i, s.ID, ids[3-i])
		}
	}
	removed, err := Prune(root, 2)
	if err != nil || len(removed) != 2 {
		t.Fatal(removed, err)
	}
	sets, _ = List(root)
	if len(sets) != 2 || sets[0].ID != ids[3] || sets[1].ID != ids[2] {
		t.Fatalf("kept %+v", sets)
	}

	e, _ := Begin(root, "")
	if err := e.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Dir); !os.IsNotExist(err) {
		t.Fatal("empty set not removed")
	}
	if sets, _ := List(t.TempDir() + "/missing"); len(sets) != 0 {
		t.Fatal("missing root")
	}
	if _, err := Latest(t.TempDir()); err == nil {
		t.Fatal("expected error for no backups")
	}
}
