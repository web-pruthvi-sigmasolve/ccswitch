// Package testutil builds fake Claude Desktop stores in temp directories for
// tests across ccswitch packages. It deliberately has no dependency on the
// other internal packages.
package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// Fixture is a fake home directory containing a Claude Desktop user-data
// folder and a Claude Code transcript folder.
type Fixture struct {
	t testing.TB
	// Home is the fake home directory.
	Home string
	// UserData is Home/.config/Claude.
	UserData string
	// TranscriptRoot is Home/.claude/projects.
	TranscriptRoot string
}

// Entry describes one sidebar file to create with AddEntry.
type Entry struct {
	SessionID      string // "local_<uuid>"; also the file stem
	CLISessionID   string
	Title          string
	Cwd            string
	CreatedAt      int64 // defaults to LastActivityAt
	LastActivityAt int64
	LastFocusedAt  int64
	IsArchived     bool
	// Extra keys are appended after the standard ones; they must not collide.
	Extra map[string]any
}

// NewFixtureStore creates an empty fixture under t.TempDir().
func NewFixtureStore(t testing.TB) *Fixture {
	t.Helper()
	home := t.TempDir()
	f := &Fixture{
		t:              t,
		Home:           home,
		UserData:       filepath.Join(home, ".config", "Claude"),
		TranscriptRoot: filepath.Join(home, ".claude", "projects"),
	}
	if err := os.MkdirAll(f.TranscriptRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

// AddAccount creates the account folder and returns its path.
func (f *Fixture) AddAccount(uuid string) string {
	f.t.Helper()
	p := filepath.Join(f.UserData, "claude-code-sessions", uuid)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// AddBucket creates the account/org folder and returns its path.
func (f *Fixture) AddBucket(acc, org string) string {
	f.t.Helper()
	p := filepath.Join(f.UserData, "claude-code-sessions", acc, org)
	if err := os.MkdirAll(p, 0o755); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// sidebar mirrors the key set and order the real app writes.
type sidebar struct {
	SessionID                  string           `json:"sessionId"`
	CLISessionID               string           `json:"cliSessionId"`
	Cwd                        string           `json:"cwd"`
	OriginCwd                  string           `json:"originCwd"`
	GitAnchors                 []map[string]any `json:"gitAnchors"`
	LastFocusedAt              int64            `json:"lastFocusedAt"`
	CreatedAt                  int64            `json:"createdAt"`
	LastActivityAt             int64            `json:"lastActivityAt"`
	Model                      string           `json:"model"`
	Effort                     string           `json:"effort"`
	IsArchived                 bool             `json:"isArchived"`
	Title                      string           `json:"title"`
	TitleSource                string           `json:"titleSource"`
	PermissionMode             string           `json:"permissionMode"`
	RemoteMcpServersConfig     []any            `json:"remoteMcpServersConfig"`
	CompletedTurns             int              `json:"completedTurns"`
	TitleTurn                  int              `json:"titleTurn"`
	LastAssistantUUID          string           `json:"lastAssistantUuid"`
	LatestUserFrameAt          int64            `json:"latestUserFrameAt"`
	AlwaysAllowedReasons       []any            `json:"alwaysAllowedReasons"`
	SessionPermissionUpdates   []any            `json:"sessionPermissionUpdates"`
	SpawnSeed                  map[string]any   `json:"spawnSeed"`
	TerminalClaudeTabOrdinal   int              `json:"terminalClaudeTabOrdinal"`
	LastSpawnRootDetected      bool             `json:"lastSpawnRootDetected"`
	ClassifierSummaryEnabled   bool             `json:"classifierSummaryEnabled"`
	SteeredByRemoteClient      bool             `json:"steeredByRemoteClient"`
	RemoteControlAutoEligible  bool             `json:"remoteControlAutoEligible"`
	AutoChosenInApp            bool             `json:"autoChosenInApp"`
	ChromePermissionMode       string           `json:"chromePermissionMode"`
	ToolSurfaceSnapshotVersion map[string]any   `json:"toolSurfaceSnapshot"`
}

// AddEntry writes a minified single-line sidebar file (no trailing newline,
// mode 0600) in the bucket acc/org and returns its path. The file's mtime is
// set from LastActivityAt when positive so mtime ordering is meaningful.
func (f *Fixture) AddEntry(acc, org string, e Entry) string {
	f.t.Helper()
	dir := f.AddBucket(acc, org)
	created := e.CreatedAt
	if created == 0 {
		created = e.LastActivityAt
	}
	s := sidebar{
		SessionID:                  e.SessionID,
		CLISessionID:               e.CLISessionID,
		Cwd:                        e.Cwd,
		OriginCwd:                  e.Cwd,
		GitAnchors:                 []map[string]any{{"gitRoot": e.Cwd, "commonDir": e.Cwd + "/.git", "trustedAt": created}},
		LastFocusedAt:              e.LastFocusedAt,
		CreatedAt:                  created,
		LastActivityAt:             e.LastActivityAt,
		Model:                      "claude-opus-5-5",
		Effort:                     "medium",
		IsArchived:                 e.IsArchived,
		Title:                      e.Title,
		TitleSource:                "auto",
		PermissionMode:             "auto",
		RemoteMcpServersConfig:     []any{map[string]any{"uuid": "1a59c906-04da-521d-bda7-7f71b9f9e01c", "name": "Claude Docs", "url": "https://example.invalid/mcp", "tools": []any{}}},
		CompletedTurns:             3,
		LastAssistantUUID:          "00000000-0000-4000-8000-000000000001",
		LatestUserFrameAt:          e.LastActivityAt,
		AlwaysAllowedReasons:       []any{},
		SessionPermissionUpdates:   []any{},
		SpawnSeed:                  map[string]any{},
		TerminalClaudeTabOrdinal:   1,
		ChromePermissionMode:       "skip_all_permission_checks",
		ToolSurfaceSnapshotVersion: map[string]any{"appVersion": "2.0.0", "cliVersion": "2.0.0", "recordedAt": e.LastActivityAt},
	}
	data, err := json.Marshal(s)
	if err != nil {
		f.t.Fatal(err)
	}
	if len(e.Extra) > 0 {
		var existing map[string]json.RawMessage
		if err := json.Unmarshal(data, &existing); err != nil {
			f.t.Fatal(err)
		}
		keys := make([]string, 0, len(e.Extra))
		for k := range e.Extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		data = []byte(strings.TrimSuffix(string(data), "}"))
		for _, k := range keys {
			if _, dup := existing[k]; dup {
				f.t.Fatalf("testutil: Extra key %q collides with a standard key", k)
			}
			kb, _ := json.Marshal(k)
			vb, err := json.Marshal(e.Extra[k])
			if err != nil {
				f.t.Fatal(err)
			}
			data = append(data, ',')
			data = append(data, kb...)
			data = append(data, ':')
			data = append(data, vb...)
		}
		data = append(data, '}')
	}
	p := filepath.Join(dir, e.SessionID+".json")
	f.WriteFile(p, data)
	if e.LastActivityAt > 0 {
		mt := time.UnixMilli(e.LastActivityAt)
		if err := os.Chtimes(p, mt, mt); err != nil {
			f.t.Fatal(err)
		}
	}
	return p
}

// AddTranscript creates an empty-ish transcript for cwd/cliSessionID under
// TranscriptRoot and returns its path.
func (f *Fixture) AddTranscript(cwd, cliSessionID string) string {
	f.t.Helper()
	enc := strings.NewReplacer("/", "-", `\`, "-", ":", "-", " ", "-").Replace(cwd)
	p := filepath.Join(f.TranscriptRoot, enc, cliSessionID+".jsonl")
	f.WriteFile(p, []byte("{\"type\":\"summary\"}\n"))
	return p
}

// WriteFile writes data to path (mode 0600), creating parent directories.
func (f *Fixture) WriteFile(path string, data []byte) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		f.t.Fatal(err)
	}
}
