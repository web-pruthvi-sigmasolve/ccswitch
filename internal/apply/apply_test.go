package apply

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/backup"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/plan"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

type env struct {
	root, srcDir, dstDir, backups string
	src, dst                      *store.Bucket
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{root: root, backups: filepath.Join(root, "backups")}
	e.srcDir = filepath.Join(root, "sessions", "srcacct", "org")
	e.dstDir = filepath.Join(root, "sessions", "dstacct", "org")
	for _, d := range []string{e.srcDir, e.dstDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	e.src = &store.Bucket{AccountUUID: "srcacct", OrgUUID: "org", Path: e.srcDir}
	e.dst = &store.Bucket{AccountUUID: "dstacct", OrgUUID: "org", Path: e.dstDir}
	return e
}

func (e *env) put(t *testing.T, b *store.Bucket, id, raw string, activity int64) *store.Entry {
	t.Helper()
	p := filepath.Join(b.Path, id+".json")
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	en := &store.Entry{SessionID: id, Path: p, Raw: []byte(raw), LastActivityAt: activity, Mode: 0o600, TranscriptExists: true}
	b.Entries = append(b.Entries, en)
	return en
}

func (e *env) plan(t *testing.T, o plan.Options) *plan.Plan {
	t.Helper()
	p, err := plan.Build(e.src, e.dst, e.src.Entries, o)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAddCreatesExactBytes(t *testing.T) {
	e := newEnv(t)
	raw := `{"sessionId":"local_1","title":"a<b","remoteMcpServersConfig":[{"x":1}]}`
	e.put(t, e.src, "local_1", raw, 5)
	o := plan.DefaultOptions()
	o.StripAccountConfig = false
	var progress []string
	res, err := Run(e.plan(t, o), Options{BackupRoot: e.backups, Progress: func(ev Event) { progress = append(progress, ev.Status) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied != 1 || res.Failed != 0 || res.BackupSet != nil {
		t.Fatalf("%+v", res)
	}
	dst := filepath.Join(e.dstDir, "local_1.json")
	if got := read(t, dst); got != raw {
		t.Fatalf("bytes differ: %s", got)
	}
	testutil.WantPerm(t, dst, 0o600)
	if _, err := os.Stat(dst + ".ccswitch-tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file left behind")
	}
	if _, err := os.Stat(e.backups); err == nil {
		if ents, _ := os.ReadDir(e.backups); len(ents) != 0 {
			t.Fatal("empty backup set not discarded")
		}
	}
	if strings.Join(progress, ",") != "added" {
		t.Fatalf("events %v", progress)
	}
}

func TestAddStripsAccountConfig(t *testing.T) {
	e := newEnv(t)
	e.put(t, e.src, "local_1", `{"sessionId":"local_1","remoteMcpServersConfig":[{"x":1}]}`, 5)
	if _, err := Run(e.plan(t, plan.DefaultOptions()), Options{BackupRoot: e.backups}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(e.dstDir, "local_1.json")); got != `{"sessionId":"local_1","remoteMcpServersConfig":[]}` {
		t.Fatal(got)
	}
}

func TestMergeKeepsDestOwnedAndBacksUp(t *testing.T) {
	e := newEnv(t)
	e.put(t, e.src, "local_1", `{"sessionId":"local_1","title":"new","lastActivityAt":9,"lastFocusedAt":100}`, 9)
	orig := `{"sessionId":"local_1","title":"old","lastActivityAt":5,"lastFocusedAt":1}`
	e.put(t, e.dst, "local_1", orig, 5)
	res, err := Run(e.plan(t, plan.DefaultOptions()), Options{BackupRoot: e.backups, Note: "n"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sessionId":"local_1","title":"new","lastActivityAt":9,"lastFocusedAt":1}`
	if got := read(t, filepath.Join(e.dstDir, "local_1.json")); got != want {
		t.Fatalf("got %s", got)
	}
	if res.BackupSet == nil || len(res.BackupSet.Files) != 1 {
		t.Fatalf("no backup: %+v", res)
	}
	latest, err := backup.Latest(e.backups)
	if err != nil || latest.Note != "n" {
		t.Fatal(latest, err)
	}
	if got := read(t, filepath.Join(latest.Dir, "dstacct", "org", "local_1.json")); got != orig {
		t.Fatalf("backup content %s", got)
	}
	if res.Events[0].Status != "merged" {
		t.Fatal(res.Events[0].Status)
	}
}

func TestOverwriteUsesSourceBytes(t *testing.T) {
	e := newEnv(t)
	raw := "{\r\n\"sessionId\":\"local_1\",\"lastActivityAt\":9}\r\n"
	e.put(t, e.src, "local_1", raw, 9)
	e.put(t, e.dst, "local_1", `{"sessionId":"local_1","lastActivityAt":1}`, 1)
	o := plan.DefaultOptions()
	o.Mode = plan.ModeOverwrite
	res, err := Run(e.plan(t, o), Options{BackupRoot: e.backups})
	if err != nil || res.Events[0].Status != "overwritten" {
		t.Fatal(res, err)
	}
	if got := read(t, filepath.Join(e.dstDir, "local_1.json")); got != raw {
		t.Fatalf("got %q", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.put(t, e.src, "local_1", `{"sessionId":"local_1","title":"n","lastActivityAt":9}`, 9)
	e.put(t, e.src, "local_2", `{"sessionId":"local_2","lastActivityAt":1}`, 1)
	orig := `{"sessionId":"local_2","lastActivityAt":0}`
	e.put(t, e.dst, "local_2", orig, 0)
	res, err := Run(e.plan(t, plan.DefaultOptions()), Options{DryRun: true, BackupRoot: e.backups})
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupSet != nil || res.Applied != 2 {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(e.dstDir, "local_1.json")); !os.IsNotExist(err) {
		t.Fatal("dry run created a file")
	}
	if read(t, filepath.Join(e.dstDir, "local_2.json")) != orig {
		t.Fatal("dry run modified a file")
	}
	if _, err := os.Stat(e.backups); !os.IsNotExist(err) {
		t.Fatal("dry run created backups")
	}
	if res.Events[0].Status != "added" || res.Events[1].Status != "merged" {
		t.Fatalf("statuses %v %v", res.Events[0].Status, res.Events[1].Status)
	}
}

func TestVerificationFailureRestores(t *testing.T) {
	e := newEnv(t)
	e.put(t, e.src, "local_1", `{"sessionId":"local_1","title":"new","lastActivityAt":9}`, 9)
	orig := `{"sessionId":"local_1","title":"old","lastActivityAt":5}`
	e.put(t, e.dst, "local_1", orig, 5)
	e.put(t, e.src, "local_2", `{"sessionId":"local_2","lastActivityAt":9}`, 9)
	fail := func(path, id string) error {
		if id == "local_1" || id == "local_2" {
			return errors.New("boom")
		}
		return nil
	}
	res, err := Run(e.plan(t, plan.DefaultOptions()), Options{BackupRoot: e.backups, verify: fail})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 2 || res.Applied != 0 {
		t.Fatalf("%+v", res)
	}
	if got := read(t, filepath.Join(e.dstDir, "local_1.json")); got != orig {
		t.Fatalf("not restored: %s", got)
	}
	if _, err := os.Stat(filepath.Join(e.dstDir, "local_2.json")); !os.IsNotExist(err) {
		t.Fatal("failed add not removed")
	}
	var seq []string
	for _, ev := range res.Events {
		seq = append(seq, ev.Status)
	}
	if strings.Join(seq, ",") != "restored,failed,failed" {
		t.Fatalf("events %v", seq)
	}
	if res.Events[1].Err == nil {
		t.Fatal("failed event lacks error")
	}
}

func TestMissingAccountDirFails(t *testing.T) {
	e := newEnv(t)
	e.dst.Path = filepath.Join(e.root, "sessions", "ghost", "org")
	e.put(t, e.src, "local_1", `{"sessionId":"local_1"}`, 1)
	e.put(t, e.src, "local_2", `{"sessionId":"local_2"}`, 1)
	res, err := Run(e.plan(t, plan.DefaultOptions()), Options{BackupRoot: e.backups})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 2 || !strings.Contains(res.Events[0].Err.Error(), "account folder does not exist") {
		t.Fatalf("%+v", res.Events[0])
	}
	if _, err := os.Stat(filepath.Join(e.root, "sessions", "ghost")); !os.IsNotExist(err) {
		t.Fatal("account dir was created")
	}
}

func TestCreatesBucketDirWhenAccountExists(t *testing.T) {
	e := newEnv(t)
	e.dst.Path = filepath.Join(e.dstDir, "..", "neworg")
	e.put(t, e.src, "local_1", `{"sessionId":"local_1"}`, 1)
	res, err := Run(e.plan(t, plan.DefaultOptions()), Options{BackupRoot: e.backups})
	if err != nil || res.Applied != 1 {
		t.Fatal(res, err)
	}
	if st, err := os.Stat(e.dst.Path); err != nil || !st.IsDir() {
		t.Fatalf("bucket dir: %v %v", st, err)
	}
	testutil.WantPerm(t, e.dst.Path, 0o700)
}

func TestSkipAndMixedEventSequence(t *testing.T) {
	e := newEnv(t)
	e.put(t, e.src, "local_1", `{"sessionId":"local_1","lastActivityAt":1}`, 1)
	e.put(t, e.src, "local_2", `{"sessionId":"local_2","lastActivityAt":1}`, 1)
	e.put(t, e.dst, "local_1", `{"sessionId":"local_1","lastActivityAt":9}`, 9) // dest newer -> skip
	res, err := Run(e.plan(t, plan.DefaultOptions()), Options{BackupRoot: e.backups})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || res.Applied != 1 || res.Events[0].Status != "skipped" || res.Events[1].Status != "added" {
		t.Fatalf("%+v", res)
	}
}

func TestMergeFailureIsReportedPerOp(t *testing.T) {
	e := newEnv(t)
	e.put(t, e.src, "local_1", `{"sessionId":"local_1","lastActivityAt":9}`, 9)
	e.put(t, e.dst, "local_1", `{"sessionId":"local_1","lastActivityAt":1}`, 1)
	e.put(t, e.src, "local_2", `{"sessionId":"local_2"}`, 1)
	// Corrupt the destination file after planning.
	p := e.plan(t, plan.DefaultOptions())
	if err := os.WriteFile(filepath.Join(e.dstDir, "local_1.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Run(p, Options{BackupRoot: e.backups})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Applied != 1 {
		t.Fatalf("%+v", res)
	}
	if read(t, filepath.Join(e.dstDir, "local_1.json")) != "not json" {
		t.Fatal("failed op modified the file")
	}
}
