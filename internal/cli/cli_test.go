package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/backup"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/merge"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/testutil"
)

const (
	accWork     = "aaaaaaaa-1111-4111-8111-111111111111"
	accPersonal = "bbbbbbbb-2222-4222-8222-222222222222"
	org1        = "11111111-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

	idMissing   = "local_00000001-0000-4000-8000-000000000001"
	idSrcNewer  = "local_00000002-0000-4000-8000-000000000002"
	idDestNewer = "local_00000003-0000-4000-8000-000000000003"
	idArchived  = "local_00000004-0000-4000-8000-000000000004"
)

type harness struct {
	t      *testing.T
	fx     *testutil.Fixture
	cfgDir string
	paths  map[string]string // label -> file path
}

func ago(d time.Duration) int64 { return time.Now().Add(-d).UnixMilli() }

// setup builds two accounts that share one org:
//
//	work:     Missing chat, Newer in source, Dest newer, Archived chat
//	personal: Newer in source (older, with its own lastFocusedAt and extra key), Dest newer (newer)
func setup(t *testing.T) *harness {
	t.Helper()
	fx := testutil.NewFixtureStore(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(fx.Home, ".claude"))
	t.Setenv("NO_COLOR", "1")
	h := &harness{t: t, fx: fx, cfgDir: t.TempDir(), paths: map[string]string{}}

	add := func(label, acc, id, cli, title, cwd string, act time.Duration, extra testutil.Entry) {
		e := extra
		e.SessionID, e.CLISessionID, e.Title, e.Cwd, e.LastActivityAt = id, cli, title, cwd, ago(act)
		h.paths[label] = fx.AddEntry(acc, org1, e)
		if cwd != "" && cli != "" {
			fx.AddTranscript(cwd, cli)
		}
	}
	add("work-missing", accWork, idMissing, "cli-1", "Missing chat", "/proj/shop", 1*time.Hour, testutil.Entry{})
	add("work-srcnewer", accWork, idSrcNewer, "cli-2", "Newer in source", "/proj/api", 2*time.Hour, testutil.Entry{LastFocusedAt: 111})
	add("work-destnewer", accWork, idDestNewer, "cli-3", "Dest newer (work title)", "/proj/api", 72*time.Hour, testutil.Entry{})
	add("work-archived", accWork, idArchived, "cli-4", "Archived chat", "/proj/shop", 400*time.Hour, testutil.Entry{IsArchived: true, Extra: map[string]any{"zzCustom": []any{1, "two"}}})
	add("pers-srcnewer", accPersonal, idSrcNewer, "cli-2", "Old title", "/proj/api", 48*time.Hour, testutil.Entry{LastFocusedAt: 777, Extra: map[string]any{"destOnlyKey": "keep"}})
	add("pers-destnewer", accPersonal, idDestNewer, "cli-3", "Dest newer (personal title)", "/proj/api", 3*time.Hour, testutil.Entry{})

	cfg := &config.Config{Labels: map[string]string{accWork: "work", accPersonal: "personal"}, BackupRetention: 20}
	if err := cfg.Save(h.cfgDir); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) base() []string {
	return []string{"--user-data", h.fx.UserData, "--config-dir", h.cfgDir}
}

// run executes ccswitch in-process with a non-terminal stdin.
func (h *harness) run(args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	return h.runIn("", args...)
}

func (h *harness) runIn(stdin string, args ...string) (stdout, stderr string, code int) {
	h.t.Helper()
	var out, errb bytes.Buffer
	e := newEnv(strings.NewReader(stdin), &out, &errb)
	code = e.run(append(h.base(), args...))
	return out.String(), errb.String(), code
}

func (h *harness) read(label string) []byte {
	h.t.Helper()
	b, err := os.ReadFile(h.paths[label])
	if err != nil {
		h.t.Fatal(err)
	}
	return b
}

func contains(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("output missing %q:\n%s", sub, s)
		}
	}
}

func destFile(h *harness, id string) string {
	return filepath.Join(h.fx.UserData, "claude-code-sessions", accPersonal, org1, id+".json")
}

func jsonKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestListPlain(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("list")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	contains(t, out, "[1] work", "[2] personal", "(aaaaaaaa", "4 chats", "2 chats", "last active", "projects:", "api", "Missing chat")
}

func TestListJSON(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("list", "--json")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	var got []struct {
		Index       int      `json:"index"`
		UUID        string   `json:"uuid"`
		Label       string   `json:"label"`
		LabelSource string   `json:"labelSource"`
		Current     bool     `json:"current"`
		Chats       int      `json:"chats"`
		LastActive  string   `json:"lastActive"`
		Projects    []string `json:"projects"`
		Buckets     []struct {
			Org   string `json:"org"`
			Path  string `json:"path"`
			Chats int    `json:"chats"`
		} `json:"buckets"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got[0].UUID != accWork || got[0].Index != 1 || got[0].Label != "work" || got[0].LabelSource != "user" || got[0].Chats != 4 {
		t.Fatalf("unexpected: %+v", got)
	}
	if len(got[0].Buckets) != 1 || got[0].Buckets[0].Org != org1 || got[0].Buckets[0].Chats != 4 || got[0].LastActive == "" {
		t.Fatalf("unexpected buckets: %+v", got[0])
	}
}

func TestListStoreMissing(t *testing.T) {
	h := setup(t)
	var out, errb bytes.Buffer
	e := newEnv(strings.NewReader(""), &out, &errb)
	code := e.run([]string{"--user-data", t.TempDir(), "--config-dir", h.cfgDir, "list"})
	if code != 4 {
		t.Fatalf("code %d, want 4", code)
	}
	contains(t, errb.String(), "Folders checked", "no claude-code-sessions folder", "hint:")
}

func TestChatsFilters(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("chats", "--account", "work")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, out, "shop (1 chat)", "api (2 chats)", "Missing chat", "Newer in source", "Dest newer (work title)", chatID(idMissing))
	if strings.Contains(out, "Archived chat") {
		t.Errorf("archived chat must be hidden by default:\n%s", out)
	}

	out, _, _ = h.run("chats", "--account", "work", "--archived")
	contains(t, out, "Archived chat", "(archived)")

	out, _, _ = h.run("chats", "--account", "1", "--project", "API")
	contains(t, out, "Newer in source")
	if strings.Contains(out, "Missing chat") {
		t.Errorf("project filter leaked:\n%s", out)
	}

	out, _, _ = h.run("chats", "--account", "work", "--since", "24h")
	contains(t, out, "Missing chat", "Newer in source")
	if strings.Contains(out, "Dest newer") {
		t.Errorf("since filter leaked:\n%s", out)
	}

	out, _, code = h.run("chats", "--account", "work", "--json", "--archived")
	if code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(out, `"raw"`) || strings.Contains(strings.ToLower(out), "remotemcpserversconfig") {
		t.Errorf("JSON must not include raw data:\n%s", out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0]["sessionId"] != idMissing || rows[0]["org"] != org1 || rows[0]["transcriptExists"] != true {
		t.Fatalf("unexpected rows: %v", rows)
	}

	_, errs, code := h.run("chats", "--account", "nobody")
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	contains(t, errs, "No account matches", "hint:")
	_, _, code = h.run("chats")
	if code != 1 {
		t.Fatalf("missing --account: code %d", code)
	}
}

func TestSyncDryRun(t *testing.T) {
	h := setup(t)
	before := h.read("pers-srcnewer")
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--dry-run")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	contains(t, out,
		"+ Missing chat (shop)",
		"~ Newer in source (api)",
		"! Dest newer (work title) (api)",
		"differs:",
		"1 to add", "1 to merge", "1 kept (destination newer)",
		"Dry run: nothing was written")
	if strings.Contains(out, "Archived chat") {
		t.Errorf("archived chat must not be selected:\n%s", out)
	}
	if _, err := os.Stat(destFile(h, idMissing)); !os.IsNotExist(err) {
		t.Fatal("dry run created a file")
	}
	if !bytes.Equal(before, h.read("pers-srcnewer")) {
		t.Fatal("dry run changed a file")
	}
	if sets, _ := backup.List(config.BackupDir(h.cfgDir)); len(sets) != 0 {
		t.Fatal("dry run made a backup")
	}
}

func TestSyncDryRunJSON(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	var doc struct {
		DryRun bool `json:"dryRun"`
		Plan   struct {
			Adds, Merges, Conflicts int
			Ops                     []struct{ Action, Class string }
		} `json:"plan"`
		Result any `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !doc.DryRun || doc.Plan.Adds != 1 || doc.Plan.Merges != 1 || doc.Plan.Conflicts != 1 || len(doc.Plan.Ops) != 3 || doc.Result != nil {
		t.Fatalf("unexpected: %+v", doc)
	}
}

func TestSyncApplyAndUndo(t *testing.T) {
	h := setup(t)
	srcMissing := h.read("work-missing")
	srcNewer := h.read("work-srcnewer")
	mergedBefore := h.read("pers-srcnewer")
	destNewerBefore := h.read("pers-destnewer")

	out, errs, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--yes")
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errs)
	}
	contains(t, out, "added", "merged", "Done: 2 applied", "Backup:", "Switch account or restart Claude Desktop to see the chats")

	// Added: bytes equal the stripped source.
	want, err := merge.PrepareAdd(srcMissing, merge.DefaultPolicy(), true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(destFile(h, idMissing))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("added file differs from stripped source:\n got %s\nwant %s", got, want)
	}
	if bytes.Equal(got, srcMissing) || !bytes.Contains(got, []byte(`"remoteMcpServersConfig":[]`)) {
		t.Fatal("remoteMcpServersConfig should have been stripped")
	}

	// Merged: source fields in, destination-owned and unknown keys kept.
	merged := h.read("pers-srcnewer")
	var mv map[string]any
	if err := json.Unmarshal(merged, &mv); err != nil {
		t.Fatal(err)
	}
	if mv["title"] != "Newer in source" || mv["destOnlyKey"] != "keep" || mv["lastFocusedAt"].(float64) != 777 {
		t.Fatalf("bad merge result: %s", merged)
	}
	if bytes.Equal(merged, mergedBefore) || bytes.Equal(merged, srcNewer) {
		t.Fatal("merge should differ from both inputs")
	}

	// DestNewer is kept byte for byte.
	if !bytes.Equal(destNewerBefore, h.read("pers-destnewer")) {
		t.Fatal("destination-newer chat was modified")
	}

	// One backup set holding the merged file's original.
	sets, err := backup.List(config.BackupDir(h.cfgDir))
	if err != nil || len(sets) != 1 {
		t.Fatalf("sets = %d, %v", len(sets), err)
	}
	if len(sets[0].Files) != 1 || !strings.Contains(sets[0].Note, "work -> personal") {
		t.Fatalf("unexpected set: %+v", sets[0])
	}

	// A second run has nothing left to do.
	if _, _, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--yes"); code != 2 {
		t.Fatalf("second sync code %d, want 2", code)
	}

	// Undo restores the merged file byte for byte.
	list, _, code := h.run("undo", "--list")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, list, sets[0].ID, "1 file(s)")
	out, _, code = h.run("undo", "--yes")
	if code != 0 {
		t.Fatalf("undo code %d\n%s", code, out)
	}
	contains(t, out, "Restored 1 file(s)", sets[0].ID)
	if !bytes.Equal(mergedBefore, h.read("pers-srcnewer")) {
		t.Fatal("undo did not restore the original bytes")
	}
	// The added chat stays (nothing is ever deleted).
	if _, err := os.Stat(destFile(h, idMissing)); err != nil {
		t.Fatal("undo must not delete added chats")
	}
}

func TestSyncModesAndPolicies(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--mode", "add-only", "--dry-run")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, out, "1 to add", "0 to merge")

	out, _, code = h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--on-conflict", "prefer-source", "--dry-run")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, out, "2 to merge", "0 kept")

	_, errs, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--mode", "bogus")
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	contains(t, errs, "Unknown mode", "hint:")
}

func TestSyncAllIncludesArchivedByDefault(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--yes")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	contains(t, out, "+ Archived chat (shop)", "2 to add", "Done: 3 applied")
	if _, err := os.Stat(destFile(h, idArchived)); err != nil {
		t.Fatalf("archived chat was not added: %v", err)
	}
}

func TestSyncSkipArchived(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--dry-run")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	contains(t, out, "1 to add")
	if strings.Contains(out, "Archived chat") {
		t.Errorf("--skip-archived still selected the archived chat:\n%s", out)
	}
	// Selection by project includes archived chats too, unless skipped.
	out, _, _ = h.run("sync", "--from", "work", "--to", "personal", "--project", "shop", "--dry-run")
	contains(t, out, "Missing chat", "Archived chat")
	out, _, _ = h.run("sync", "--from", "work", "--to", "personal", "--project", "shop", "--skip-archived", "--dry-run")
	if strings.Contains(out, "Archived chat") {
		t.Errorf("--skip-archived ignored with --project:\n%s", out)
	}
}

func TestChatsNotesHiddenArchived(t *testing.T) {
	h := setup(t)
	out, _, _ := h.run("chats", "--account", "work")
	contains(t, out, "(1 archived chats hidden; add --archived to list them)")
	out, _, _ = h.run("chats", "--account", "work", "--archived")
	if strings.Contains(out, "archived chats hidden") {
		t.Errorf("note printed although archived chats are listed:\n%s", out)
	}
}

func TestSyncAlsoInOtherOrgBucket(t *testing.T) {
	h := setup(t)
	const org2 = "22222222-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	h.fx.AddEntry(accPersonal, org2, testutil.Entry{SessionID: idMissing, CLISessionID: "cli-1", Title: "Missing chat", Cwd: "/proj/shop", LastActivityAt: ago(1 * time.Hour)})
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--id", chatID(idMissing), "--dry-run", "--json")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	var doc struct {
		Plan struct {
			Ops []struct {
				Action, Reason, AlsoInOrg string
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Plan.Ops) != 1 || doc.Plan.Ops[0].Action != "Add" || doc.Plan.Ops[0].AlsoInOrg != org2 {
		t.Fatalf("unexpected plan: %+v", doc.Plan.Ops)
	}
	out, _, _ = h.run("sync", "--from", "work", "--to", "personal", "--id", chatID(idMissing), "--dry-run")
	contains(t, out, "+ Missing chat (shop)", "not in this org yet; already in this account's 22222222… bucket")
}

func TestSyncOrphanHintAndWillChange(t *testing.T) {
	h := setup(t)
	const idOrphan = "local_00000009-0000-4000-8000-000000000009"
	h.fx.AddEntry(accWork, org1, testutil.Entry{SessionID: idOrphan, CLISessionID: "cli-9", Title: "Orphan chat", Cwd: "/proj/none", LastActivityAt: ago(5 * time.Hour)})
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--id", chatID(idOrphan), "--dry-run")
	if code != exitNothing {
		t.Fatalf("code %d\n%s", code, out)
	}
	contains(t, out, "1 orphans skipped", "Use --include-orphans to copy chats whose transcript is missing.", "Nothing to do")

	out, _, _ = h.run("sync", "--from", "work", "--to", "personal", "--id", chatID(idSrcNewer), "--dry-run")
	contains(t, out, "will change:")
	if strings.Contains(out, "differs:") || strings.Contains(out, "remoteMcpServersConfig") {
		t.Errorf("merge line must say 'will change' and omit destination-owned keys:\n%s", out)
	}
	out, _, _ = h.run("sync", "--from", "work", "--to", "personal", "--id", chatID(idDestNewer), "--dry-run")
	contains(t, out, "differs:")
}

func TestSyncSelectionFlags(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("sync", "--from", "work", "--to", "personal", "--id", chatID(idMissing), "--dry-run")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, out)
	}
	contains(t, out, "Missing chat", "1 to add")
	if strings.Contains(out, "Newer in source") {
		t.Errorf("id filter leaked:\n%s", out)
	}
	out, _, code = h.run("sync", "--from", "work", "--to", "personal", "--title", "newer in", "--dry-run")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	contains(t, out, "Newer in source")
	_, _, code = h.run("sync", "--from", "work", "--to", "personal", "--project", "nothing-here", "--dry-run")
	if code != 2 {
		t.Fatalf("no match code %d, want 2", code)
	}
}

func TestSyncErrors(t *testing.T) {
	h := setup(t)
	_, errs, code := h.run("sync", "--from", "work", "--to", "personal")
	if code != 1 {
		t.Fatalf("no filter: code %d", code)
	}
	contains(t, errs, "Say which chats", "--all")

	_, errs, code = h.run("sync", "--from", "work", "--to", "personal", "--all")
	if code != 1 {
		t.Fatalf("no --yes: code %d", code)
	}
	contains(t, errs, "Refusing", "--yes")
	if _, err := os.Stat(destFile(h, idMissing)); !os.IsNotExist(err) {
		t.Fatal("refused run still wrote")
	}

	_, errs, code = h.run("sync", "--from", "work", "--to", "work", "--all", "--dry-run")
	if code != 1 {
		t.Fatalf("same account: code %d", code)
	}
	contains(t, errs, "same folder")

	_, _, code = h.run("sync", "--to", "personal", "--all")
	if code != 1 {
		t.Fatalf("missing --from: code %d", code)
	}
	_, errs, code = h.run("sync", "--from", "work", "--to", "personal", "--all", "--bogus")
	if code != 1 {
		t.Fatalf("unknown flag: code %d", code)
	}
	contains(t, errs, "Unknown flag", "hint:")
}

func TestSyncConfirmPrompt(t *testing.T) {
	h := setup(t)
	args := append(h.base(), "sync", "--from", "work", "--to", "personal", "--all", "--skip-archived")

	var out, errb bytes.Buffer
	e := newEnv(strings.NewReader("n\n"), &out, &errb)
	e.stdinTTY = true
	if code := e.run(args); code != 2 {
		t.Fatalf("declined: code %d\n%s", code, out.String())
	}
	contains(t, out.String(), "Apply 2 change(s)", "Cancelled")
	if _, err := os.Stat(destFile(h, idMissing)); !os.IsNotExist(err) {
		t.Fatal("declined run wrote a file")
	}

	out.Reset()
	e = newEnv(strings.NewReader("yes\n"), &out, &errb)
	e.stdinTTY = true
	if code := e.run(args); code != 0 {
		t.Fatalf("accepted: code %d\n%s%s", code, out.String(), errb.String())
	}
	if _, err := os.Stat(destFile(h, idMissing)); err != nil {
		t.Fatal("accepted run did not write")
	}
}

func TestSyncLockHeld(t *testing.T) {
	h := setup(t)
	if err := os.WriteFile(config.LockPath(h.cfgDir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, errs, code := h.run("sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--yes")
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	contains(t, errs, "in progress", config.LockPath(h.cfgDir))
	if _, err := os.Stat(destFile(h, idMissing)); !os.IsNotExist(err) {
		t.Fatal("locked run wrote")
	}
}

func TestUnarchive(t *testing.T) {
	h := setup(t)
	before := h.read("work-archived")
	if !bytes.Contains(before, []byte(`"isArchived":true`)) {
		t.Fatal("fixture should be archived")
	}

	out, _, code := h.run("unarchive", "--account", "work", "--all", "--dry-run")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, out, "Archived chat", "(archived)", "Dry run")
	if !bytes.Equal(before, h.read("work-archived")) {
		t.Fatal("dry run changed the file")
	}

	_, errs, code := h.run("unarchive", "--account", "work", "--all")
	if code != 1 {
		t.Fatalf("no --yes: code %d", code)
	}
	contains(t, errs, "Refusing")

	out, errs, code = h.run("unarchive", "--account", "work", "--all", "--yes")
	if code != 0 {
		t.Fatalf("code %d\n%s\n%s", code, out, errs)
	}
	contains(t, out, "1 un-archived", "Backup:")
	after := h.read("work-archived")
	want := bytes.Replace(before, []byte(`"isArchived":true`), []byte(`"isArchived":false`), 1)
	if !bytes.Equal(after, want) {
		t.Fatalf("only isArchived may change:\n got %s\nwant %s", after, want)
	}
	if strings.Join(jsonKeys(t, before), ",") != strings.Join(jsonKeys(t, after), ",") {
		t.Fatal("key order changed")
	}
	testutil.WantPerm(t, h.paths["work-archived"], 0o600)
	sets, _ := backup.List(config.BackupDir(h.cfgDir))
	if len(sets) != 1 || len(sets[0].Files) != 1 {
		t.Fatalf("backup sets: %+v", sets)
	}

	// Nothing left to do.
	if _, _, code := h.run("unarchive", "--account", "work", "--all", "--yes"); code != 2 {
		t.Fatalf("second run code %d, want 2", code)
	}
	// Undo brings the flag back.
	if _, _, code := h.run("undo", "--yes"); code != 0 {
		t.Fatal(code)
	}
	if !bytes.Equal(before, h.read("work-archived")) {
		t.Fatal("undo did not restore the archived file")
	}
}

func TestUnarchiveNeedsSelection(t *testing.T) {
	h := setup(t)
	_, errs, code := h.run("unarchive", "--account", "work", "--yes")
	if code != 1 {
		t.Fatalf("code %d", code)
	}
	contains(t, errs, "Say which chats")
}

func TestLabel(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("label")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, out, "[1] work", "[2] personal")

	if _, _, code := h.run("label", "2", "Side gig"); code != 0 {
		t.Fatal(code)
	}
	cfg, err := config.Load(h.cfgDir)
	if err != nil || cfg.Labels[accPersonal] != "Side gig" {
		t.Fatalf("labels = %v, %v", cfg, err)
	}
	out, _, _ = h.run("list")
	contains(t, out, "[2] Side gig")

	if _, _, code := h.run("label", "--clear", "Side gig"); code != 0 {
		t.Fatal(code)
	}
	cfg, _ = config.Load(h.cfgDir)
	if _, ok := cfg.Labels[accPersonal]; ok {
		t.Fatal("label not cleared")
	}
	if _, _, code := h.run("label", "1"); code != 1 {
		t.Fatalf("label without name: code %d", code)
	}
	if _, _, code := h.run("label", "1", "  "); code != 1 {
		t.Fatalf("empty name: code %d", code)
	}
}

func TestUndoNoBackups(t *testing.T) {
	h := setup(t)
	if _, _, code := h.run("undo", "--yes"); code != 2 {
		t.Fatalf("code %d, want 2", code)
	}
	out, _, code := h.run("undo", "--list")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, out, "No backups yet")
}

func TestDoctor(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("doctor")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	contains(t, out, "ccswitch dev", "Candidates examined", h.fx.UserData, "chosen", "transcript root", "(exists)",
		"accounts", "chats", "unreadable entries", "Claude Desktop running", "Claude Desktop version", "2.0.0", "store writable", "yes")
	// The probe file is gone.
	ents, _ := os.ReadDir(filepath.Join(h.fx.UserData, "claude-code-sessions", accWork, org1))
	for _, en := range ents {
		if strings.HasPrefix(en.Name(), ".ccswitch-doctor") {
			t.Fatal("doctor left its probe file behind")
		}
	}
	if strings.Contains(out, "Missing chat") || strings.Contains(out, "remoteMcp") {
		t.Fatal("doctor must not print chat contents")
	}
}

func TestDoctorCollapsesMissingCandidates(t *testing.T) {
	cands := []store.Candidate{
		{Path: "/home/u/.config/Claude", Reason: "chosen: 2 chats", Chosen: true},
		{Path: "/home/u/.var/app/com.anthropic.Claude/config/Claude", Reason: "no claude-code-sessions folder"},
		{Path: "/home/u/snap/a/current/.config/Claude", Reason: "folder does not exist"},
		{Path: "/home/u/snap/b/current/.config/Claude", Reason: "folder does not exist"},
		{Path: "/home/u/.config/flatpak/Claude", Reason: "folder does not exist"},
	}
	var buf bytes.Buffer
	printCandidates(&buf, cands)
	out := buf.String()
	contains(t, out, "/home/u/.config/Claude", "no claude-code-sessions folder", "  3 other locations checked (snap, flatpak, …) do not exist")
	if strings.Contains(out, "snap/a") || strings.Contains(out, "folder does not exist") {
		t.Errorf("non-existent candidates must be collapsed:\n%s", out)
	}
	buf.Reset()
	printCandidates(&buf, nil)
	contains(t, buf.String(), "(none)")
}

func TestDoctorWithoutStore(t *testing.T) {
	h := setup(t)
	empty := t.TempDir()
	var out, errb bytes.Buffer
	e := newEnv(strings.NewReader(""), &out, &errb)
	code := e.run([]string{"--user-data", empty, "--config-dir", h.cfgDir, "doctor"})
	if code != 0 {
		t.Fatalf("code %d\n%s", code, errb.String())
	}
	contains(t, out.String(), "Candidates examined", empty, "no claude-code-sessions folder", "(none found)", "No session store was found")
}

func TestVersionAndHelp(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("version")
	if code != 0 || strings.TrimSpace(out) != "ccswitch dev (none, unknown)" {
		t.Fatalf("version: %d %q", code, out)
	}
	out, _, code = h.run("--help")
	if code != 0 {
		t.Fatal(code)
	}
	contains(t, out, "list", "chats", "sync", "unarchive", "label", "undo", "doctor", "version", "completion", "--user-data", "--config-dir", "--json", "--no-color", "--ascii", "--quiet")
	if _, errs, code := h.run("nonsense"); code != 1 {
		t.Fatalf("unknown command: %d %s", code, errs)
	}
	out, _, code = h.run("completion", "bash")
	if code != 0 || !strings.Contains(out, "bash completion") {
		t.Fatalf("completion: %d", code)
	}
}

func TestQuiet(t *testing.T) {
	h := setup(t)
	out, _, code := h.run("--quiet", "sync", "--from", "work", "--to", "personal", "--all", "--skip-archived", "--yes")
	if code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(out, "Switch account") || strings.Contains(out, "added ") {
		t.Errorf("--quiet should hide notes and progress:\n%s", out)
	}
	contains(t, out, "Done:")
}
