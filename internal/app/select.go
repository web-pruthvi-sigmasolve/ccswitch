package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
)

// ErrNoMatch is wrapped by Select when a non-empty filter matched nothing.
var ErrNoMatch = errors.New("no chats matched")

// ErrNoFilter is wrapped by Select when no filter and no All was given.
var ErrNoFilter = errors.New("no chat selection given")

// Filter chooses chats from a bucket. All criteria are combined with AND.
type Filter struct {
	// All selects every chat when no other criterion is set.
	All bool
	// Projects match Entry.Project() case-insensitively, or the end of ProjectDir.
	Projects []string
	// Since keeps chats whose Activity() is within this duration of now.
	Since time.Duration
	// IDs are "local_<uuid>", "<uuid>" or a unique prefix (4+ characters).
	IDs []string
	// TitleMatch is a case-insensitive substring of the title.
	TitleMatch string
	// SkipArchived leaves archived chats out. By default archived chats are
	// selected like any other. Chats picked by ID are always eligible.
	SkipArchived bool
}

func (f Filter) hasCriteria() bool {
	return len(f.Projects) > 0 || f.Since > 0 || len(f.IDs) > 0 || f.TitleMatch != ""
}

// Select returns the entries of b matching f, in bucket order (newest first).
// Archived chats are included unless f.SkipArchived is set (and the chat was
// not picked by ID). It fails when no criterion and no All is given, and when a
// non-empty filter matches nothing.
func Select(b *store.Bucket, f Filter, now time.Time) ([]*store.Entry, error) {
	if b == nil {
		return nil, NewHintError("", "There is no chat folder to select from.")
	}
	if !f.hasCriteria() && !f.All {
		return nil, &HintError{
			Msg:  "Say which chats to use.",
			Hint: "Use --all, --project <name>, --since <7d>, --id <id> or --title <text>.",
			Err:  ErrNoFilter,
		}
	}
	byID := map[string]bool{}
	for _, id := range f.IDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		e, err := matchID(b, id)
		if err != nil {
			return nil, err
		}
		byID[e.SessionID] = true
	}
	var cutoff int64
	if f.Since > 0 {
		cutoff = now.Add(-f.Since).UnixMilli()
	}
	title := strings.ToLower(f.TitleMatch)

	var out []*store.Entry
	for _, e := range b.Entries {
		picked := byID[e.SessionID]
		if len(f.IDs) > 0 && !picked {
			continue
		}
		if e.IsArchived && f.SkipArchived && !picked {
			continue
		}
		if len(f.Projects) > 0 && !matchesProject(e, f.Projects) {
			continue
		}
		if cutoff > 0 && e.Activity() < cutoff {
			continue
		}
		if title != "" && !strings.Contains(strings.ToLower(e.Title), title) {
			continue
		}
		out = append(out, e)
	}
	if len(out) == 0 && f.hasCriteria() {
		return nil, &HintError{
			Msg:  "No chats match that selection.",
			Hint: "Run `ccswitch chats --account <account>` to see what is there (archived chats are listed with --archived).",
			Err:  ErrNoMatch,
		}
	}
	return out, nil
}

func matchID(b *store.Bucket, id string) (*store.Entry, error) {
	low := strings.ToLower(id)
	for _, e := range b.Entries {
		if e.SessionID == low || e.SessionID == "local_"+low {
			return e, nil
		}
	}
	if len(low) < 4 {
		return nil, &HintError{Msg: fmt.Sprintf("The chat id %q is too short.", id), Hint: "Use at least 4 characters of the id.", Err: ErrNoMatch}
	}
	var found []*store.Entry
	for _, e := range b.Entries {
		if strings.HasPrefix(e.SessionID, low) || strings.HasPrefix(e.SessionID, "local_"+low) {
			found = append(found, e)
		}
	}
	switch len(found) {
	case 0:
		return nil, &HintError{Msg: fmt.Sprintf("No chat has the id %q.", id), Hint: "Run `ccswitch chats --account <account>` to see the ids.", Err: ErrNoMatch}
	case 1:
		return found[0], nil
	}
	return nil, NewHintError("Use a longer prefix.", "The chat id %q matches %d chats.", id, len(found))
}

func matchesProject(e *store.Entry, projects []string) bool {
	dir := strings.ToLower(filepath.ToSlash(e.ProjectDir()))
	name := strings.ToLower(e.Project())
	for _, p := range projects {
		p = strings.ToLower(strings.Trim(filepath.ToSlash(strings.TrimSpace(p)), "/"))
		if p == "" {
			continue
		}
		if name == p || dir == p || strings.HasSuffix(dir, "/"+p) {
			return true
		}
	}
	return false
}

var durRe = regexp.MustCompile(`^(\d+)([dw])$`)

// ParseDuration parses "7d", "2w", "24h", "30m" or any Go duration. The result
// must be positive.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, NewHintError("Examples: 7d, 2w, 24h, 30m.", "No duration was given.")
	}
	var d time.Duration
	if m := durRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, NewHintError("Examples: 7d, 2w, 24h, 30m.", "%q is not a valid duration.", s)
		}
		d = time.Duration(n) * 24 * time.Hour
		if m[2] == "w" {
			d *= 7
		}
	} else {
		var err error
		d, err = time.ParseDuration(s)
		if err != nil {
			return 0, NewHintError("Examples: 7d, 2w, 24h, 30m.", "%q is not a valid duration.", s)
		}
	}
	if d <= 0 {
		return 0, NewHintError("Examples: 7d, 2w, 24h, 30m.", "The duration must be greater than zero.")
	}
	return d, nil
}
