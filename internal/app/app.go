// Package app is the shared bootstrap used by the CLI and the TUI: it
// discovers the store, loads the configuration, resolves accounts and buckets
// from user input and selects chats with filters.
package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ccswitch/ccswitch/internal/config"
	"github.com/ccswitch/ccswitch/internal/identity"
	"github.com/ccswitch/ccswitch/internal/store"
)

// HintError is an error for the user: one plain sentence plus an optional
// hint line on how to fix it.
type HintError struct {
	// Msg is the plain sentence.
	Msg string
	// Hint is a short suggestion; it may be empty.
	Hint string
	// Err is an optional wrapped error (for errors.Is / errors.As).
	Err error
}

// Error returns the plain sentence.
func (e *HintError) Error() string { return e.Msg }

// Unwrap returns the wrapped error, if any.
func (e *HintError) Unwrap() error { return e.Err }

// NewHintError builds a HintError from a formatted message and a hint.
func NewHintError(hint, format string, args ...any) *HintError {
	return &HintError{Msg: fmt.Sprintf(format, args...), Hint: hint}
}

// App is the loaded application state.
type App struct {
	// Store is the discovered store. It is non-nil even when StoreErr is set
	// (then it only carries the discovery report).
	Store *store.Store
	// Config is the user configuration.
	Config *config.Config
	// ConfigDir is ccswitch's own folder (labels, backups, lock).
	ConfigDir string
	// Resolver names accounts.
	Resolver *identity.Resolver
	// StoreErr is non-nil when no session store was found; commands that do
	// not need the store (doctor, version) still work.
	StoreErr error
}

// LoadOptions configure Load.
type LoadOptions struct {
	// UserData is the --user-data flag. CLAUDE_USER_DATA is honoured by
	// discovery itself.
	UserData string
	// ConfigDir is the --config-dir flag; empty means the platform default.
	ConfigDir string
	// NoLock tells Load the caller will not take the lock (read-only use).
	NoLock bool
}

// Load discovers the store, loads the configuration and builds the resolver.
// A missing store is not an error: it is reported in App.StoreErr.
func Load(o LoadOptions) (*App, error) {
	dir := o.ConfigDir
	if dir == "" {
		dir = config.DefaultDir()
	}
	st, err := store.Discover(store.Options{UserDataOverride: o.UserData})
	var storeErr error
	if err != nil {
		if !errors.Is(err, store.ErrStoreNotFound) {
			return nil, &HintError{
				Msg:  fmt.Sprintf("Could not read the Claude Desktop session store: %v.", err),
				Hint: "Run `ccswitch doctor` for details.",
				Err:  err,
			}
		}
		storeErr = err
	}
	if st == nil {
		st = &store.Store{}
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, &HintError{
			Msg:  fmt.Sprintf("Could not read the ccswitch configuration: %v.", err),
			Hint: "Check or remove " + filepath.Join(dir, "config.json") + ".",
			Err:  err,
		}
	}
	migrateLegacy(cfg, dir)
	a := &App{Store: st, Config: cfg, ConfigDir: dir, StoreErr: storeErr}
	a.Resolver = identity.New(st, cfg)
	return a, nil
}

// migrateLegacy imports labels from the bash prototype's accounts.conf found in
// the config folder or next to the executable. Existing labels win. The result
// is saved best effort; failure to save is not an error.
func migrateLegacy(cfg *config.Config, dir string) {
	paths := []string{filepath.Join(dir, "accounts.conf")}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "accounts.conf"))
	}
	changed := false
	for _, p := range paths {
		for k, v := range config.MigrateLegacyLabels(p) {
			uuid, _, _ := strings.Cut(k, "/")
			if !store.IsUUID(uuid) {
				continue
			}
			if _, ok := cfg.Labels[uuid]; ok {
				continue
			}
			cfg.SetLabel(uuid, v)
			changed = true
		}
	}
	if changed {
		_ = cfg.Save(dir)
	}
}

// RequireStore returns an error wrapping store.ErrStoreNotFound when the store
// is missing.
func (a *App) RequireStore() error {
	if a.StoreErr != nil {
		return &HintError{
			Msg:  "No Claude Desktop session store was found.",
			Hint: "Run `ccswitch doctor` to see what was checked, or pass --user-data <dir> (or set CLAUDE_USER_DATA).",
			Err:  a.StoreErr,
		}
	}
	return nil
}

// ResolveAccount finds an account from user input: a 1-based number in the
// `list` order, a full UUID, a unique UUID prefix of at least 6 characters, or
// a case-insensitive label or email.
func (a *App) ResolveAccount(ref string) (*store.Account, error) {
	if err := a.RequireStore(); err != nil {
		return nil, err
	}
	ref = strings.TrimSpace(ref)
	accs := a.Store.Accounts
	if ref == "" {
		return nil, NewHintError("Use the number from `ccswitch list`, a UUID prefix or a label.", "No account was given.")
	}
	if n, err := strconv.Atoi(ref); err == nil && len(ref) < 6 {
		if n < 1 || n > len(accs) {
			return nil, NewHintError("Run `ccswitch list` to see the numbers.", "There is no account number %d (there are %d).", n, len(accs))
		}
		return accs[n-1], nil
	}
	low := strings.ToLower(ref)
	for _, acc := range accs {
		if acc.UUID == low {
			return acc, nil
		}
	}
	var matches []*store.Account
	for _, acc := range accs {
		l := a.Resolver.Label(acc.UUID)
		if strings.EqualFold(l.Name, ref) || (l.Email != "" && strings.EqualFold(l.Email, ref)) {
			matches = append(matches, acc)
		}
	}
	if len(matches) == 0 && len(low) >= 6 {
		for _, acc := range accs {
			if strings.HasPrefix(acc.UUID, low) {
				matches = append(matches, acc)
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, NewHintError("Run `ccswitch list` to see the accounts.", "No account matches %q.", ref)
	case 1:
		return matches[0], nil
	}
	var lines []string
	for _, m := range matches {
		lines = append(lines, fmt.Sprintf("[%d] %s (%s)", a.IndexOf(m), a.Resolver.Display(m), shortUUID(m.UUID)))
	}
	return nil, NewHintError("Use the account number or a longer UUID prefix.", "%q matches more than one account: %s.", ref, strings.Join(lines, "; "))
}

// IndexOf returns the 1-based position of acc in the list order (0 if absent).
func (a *App) IndexOf(acc *store.Account) int {
	for i, x := range a.Store.Accounts {
		if x == acc {
			return i + 1
		}
	}
	return 0
}

func shortUUID(u string) string {
	if len(u) > 8 {
		u = u[:8]
	}
	return u + "…"
}

// PrimaryBucket returns the account's most active bucket (newest activity,
// then most chats), or nil when the account has none.
func (a *App) PrimaryBucket(acc *store.Account) *store.Bucket {
	var best *store.Bucket
	for _, b := range acc.Buckets {
		switch {
		case best == nil:
			best = b
		case b.LastActivity() > best.LastActivity():
			best = b
		case b.LastActivity() == best.LastActivity() && len(b.Entries) > len(best.Entries):
			best = b
		}
	}
	return best
}

// BucketFor returns the bucket of acc for org. An empty org means the primary
// bucket. When org is given and the account has no such bucket, an account with
// no buckets at all gets a not-yet-existing bucket (Path = <acc.Path>/<org>,
// created by apply); an account with other buckets is an error.
func (a *App) BucketFor(acc *store.Account, org string) (*store.Bucket, error) {
	if org == "" {
		if b := a.PrimaryBucket(acc); b != nil {
			return b, nil
		}
		return nil, NewHintError("Pass --org <uuid> to choose the folder to create.", "Account %s has no chats folder yet.", shortUUID(acc.UUID))
	}
	if b := acc.Bucket(org); b != nil {
		return b, nil
	}
	if len(acc.Buckets) == 0 {
		if !store.IsUUID(org) {
			return nil, NewHintError("An organisation is identified by a UUID.", "%q is not a valid organisation UUID.", org)
		}
		return &store.Bucket{AccountUUID: acc.UUID, OrgUUID: org, Path: filepath.Join(acc.Path, org)}, nil
	}
	var orgs []string
	for _, b := range acc.Buckets {
		orgs = append(orgs, b.OrgUUID)
	}
	return nil, NewHintError("Known organisations: "+strings.Join(orgs, ", ")+".", "Account %s has no folder for organisation %s.", shortUUID(acc.UUID), org)
}

// DestBucket picks the destination bucket for moving chats out of src: the
// --org override if given, else the destination's bucket for the same org as
// the source, else its primary bucket, else (no buckets at all) a new bucket
// for the source's org.
func (a *App) DestBucket(dst *store.Account, src *store.Bucket, orgFlag string) (*store.Bucket, error) {
	if orgFlag != "" {
		return a.BucketFor(dst, orgFlag)
	}
	if b := dst.Bucket(src.OrgUUID); b != nil {
		return b, nil
	}
	if len(dst.Buckets) == 0 {
		return a.BucketFor(dst, src.OrgUUID)
	}
	return a.BucketFor(dst, "")
}
