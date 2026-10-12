// Package store discovers the Claude Desktop user-data folder and reads the
// per-account Claude Code sidebar index ("session store").
//
// Layout on disk:
//
//	<UserData>/claude-code-sessions/<account-uuid>/<org-uuid>/local_<id>.json
//	<UserData>/claude-code-sessions/<account-uuid>/<org-uuid>/scheduled-tasks.json  (never touched)
//
// Transcripts are account-agnostic and live under TranscriptRoot:
//
//	~/.claude/projects/<encoded-cwd>/<cliSessionId>.jsonl
//
// The types in this file are the contract shared by every other package.
package store

import (
	"io/fs"
	"path/filepath"
	"time"
)

// Store is the discovered session store plus the discovery report.
type Store struct {
	// UserData is the app's user-data folder, e.g. ~/.config/Claude.
	UserData string
	// Root is UserData/claude-code-sessions.
	Root string
	// TranscriptRoot is the Claude Code projects folder, e.g. ~/.claude/projects.
	TranscriptRoot string
	// Accounts are sorted by most recent activity, newest first.
	Accounts []*Account
	// Candidates is the full discovery report (for `doctor`).
	Candidates []Candidate
}

// Candidate is one user-data location that discovery examined.
type Candidate struct {
	Path    string
	Entries int
	Buckets int
	Newest  time.Time
	// Reason explains why this candidate was or was not chosen.
	Reason string
	Chosen bool
}

// Account is one signed-in identity (an account UUID folder).
type Account struct {
	UUID    string
	Path    string
	Buckets []*Bucket
}

// Bucket is one <account>/<org> folder holding sidebar entries.
type Bucket struct {
	AccountUUID string
	OrgUUID     string
	Path        string
	// Entries are sorted by Activity(), newest first.
	Entries []*Entry
	// Unreadable lists local_*.json files that failed to parse. They are
	// never copied, merged or deleted.
	Unreadable []UnreadableEntry
}

// UnreadableEntry is a sidebar file that could not be parsed.
type UnreadableEntry struct {
	Path string
	Err  error
}

// Entry is one chat's sidebar index file. Only the fields the tool needs are
// decoded; Raw keeps the exact bytes so unknown keys are never lost.
type Entry struct {
	SessionID    string // "local_<uuid>", also the file stem
	CLISessionID string // transcript id
	Title        string
	Cwd          string
	OriginCwd    string

	CreatedAt      int64 // ms since epoch
	LastActivityAt int64 // ms since epoch
	LastFocusedAt  int64 // ms since epoch
	IsArchived     bool

	Path    string
	Raw     []byte
	ModTime time.Time
	Mode    fs.FileMode

	// TranscriptPath is the resolved .jsonl path (may be empty when unknown).
	TranscriptPath   string
	TranscriptExists bool
	// CwdExists reports whether the project folder still exists on disk.
	CwdExists bool
}

// FileName returns the entry's file name, e.g. "local_<uuid>.json".
func (e *Entry) FileName() string { return filepath.Base(e.Path) }

// ProjectDir returns the project folder the chat belongs to (OriginCwd, else Cwd).
func (e *Entry) ProjectDir() string {
	if e.OriginCwd != "" {
		return e.OriginCwd
	}
	return e.Cwd
}

// Project returns the short project name shown in lists.
func (e *Entry) Project() string {
	d := e.ProjectDir()
	if d == "" {
		return "(no project)"
	}
	return filepath.Base(filepath.Clean(d))
}

// Activity returns the best "last used" timestamp for ordering and conflict
// detection: LastActivityAt, falling back to LastFocusedAt, then CreatedAt.
func (e *Entry) Activity() int64 {
	switch {
	case e.LastActivityAt > 0:
		return e.LastActivityAt
	case e.LastFocusedAt > 0:
		return e.LastFocusedAt
	default:
		return e.CreatedAt
	}
}

// Key identifies the bucket as "<account>/<org>".
func (b *Bucket) Key() string { return b.AccountUUID + "/" + b.OrgUUID }

// Find returns the entry with the given sessionId, or nil.
func (b *Bucket) Find(sessionID string) *Entry {
	for _, e := range b.Entries {
		if e.SessionID == sessionID {
			return e
		}
	}
	return nil
}

// LastActivity is the newest Activity() across the bucket's entries (0 if none).
func (b *Bucket) LastActivity() int64 {
	var m int64
	for _, e := range b.Entries {
		if a := e.Activity(); a > m {
			m = a
		}
	}
	return m
}

// TotalEntries counts entries across all buckets.
func (a *Account) TotalEntries() int {
	n := 0
	for _, b := range a.Buckets {
		n += len(b.Entries)
	}
	return n
}

// LastActivity is the newest Activity() across all buckets (0 if none).
func (a *Account) LastActivity() int64 {
	var m int64
	for _, b := range a.Buckets {
		if x := b.LastActivity(); x > m {
			m = x
		}
	}
	return m
}

// AllEntries flattens every bucket's entries.
func (a *Account) AllEntries() []*Entry {
	var out []*Entry
	for _, b := range a.Buckets {
		out = append(out, b.Entries...)
	}
	return out
}

// Bucket returns the bucket for an org UUID, or nil.
func (a *Account) Bucket(orgUUID string) *Bucket {
	for _, b := range a.Buckets {
		if b.OrgUUID == orgUUID {
			return b
		}
	}
	return nil
}

// Account returns the account with the given UUID, or nil.
func (s *Store) Account(uuid string) *Account {
	for _, a := range s.Accounts {
		if a.UUID == uuid {
			return a
		}
	}
	return nil
}

// Buckets flattens every account's buckets.
func (s *Store) Buckets() []*Bucket {
	var out []*Bucket
	for _, a := range s.Accounts {
		out = append(out, a.Buckets...)
	}
	return out
}
