// Package identity works out a human-friendly name for each account UUID in the
// session store, using only local, best-effort sources, and builds a content
// fingerprint for accounts that cannot be named.
package identity

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/ccswitch/ccswitch/internal/config"
	"github.com/ccswitch/ccswitch/internal/store"
)

// Label is the resolved identity of one account.
type Label struct {
	// Name is the user label, or the cached profile name when there is no label.
	Name string
	// Source is the highest-priority source that knows the account:
	// "user", "current", "last-signed-in", "profile" or "".
	Source string
	// Email is the cached profile email, when found.
	Email string
}

type profile struct{ name, email string }

// Resolver resolves account labels. Data sources are read lazily, once.
type Resolver struct {
	st   *store.Store
	cfg  *config.Config
	goos string
	home string

	curOnce sync.Once
	current string

	logOnce    sync.Once
	lastSigned string

	profOnce sync.Once
	profiles map[string]profile
}

// New creates a Resolver for the store using the given config's user labels
// (cfg may be nil).
func New(st *store.Store, cfg *config.Config) *Resolver {
	home, _ := os.UserHomeDir()
	return &Resolver{st: st, cfg: cfg, goos: runtime.GOOS, home: home}
}

// WithPlatform overrides the OS and home directory used to locate the Claude
// Desktop log file (a test hook). It must be called before any lookup.
func (r *Resolver) WithPlatform(goos, home string) *Resolver {
	r.goos, r.home = goos, home
	return r
}

func (r *Resolver) userData() string {
	if r.st == nil {
		return ""
	}
	return r.st.UserData
}

// IsCurrent reports whether uuid is the account Claude Desktop last recorded as
// signed in (lastKnownAccountUuid in config.json).
func (r *Resolver) IsCurrent(uuid string) bool {
	r.curOnce.Do(func() { r.current = readLastKnownAccount(filepath.Join(r.userData(), "config.json")) })
	return uuid != "" && r.current == uuid
}

func (r *Resolver) isLastSignedIn(uuid string) bool {
	r.logOnce.Do(func() { r.lastSigned = readLastIdentity(r.logCandidates()) })
	return uuid != "" && r.lastSigned == uuid
}

func (r *Resolver) logCandidates() []string {
	ud := r.userData()
	own := filepath.Join(ud, "logs", "main.log")
	if r.goos == "darwin" && r.home != "" {
		return []string{filepath.Join(r.home, "Library", "Logs", "Claude", "main.log"), own}
	}
	return []string{own}
}

func (r *Resolver) profile(uuid string) (profile, bool) {
	r.profOnce.Do(func() { r.profiles = scrapeProfiles(filepath.Join(r.userData(), "IndexedDB")) })
	p, ok := r.profiles[uuid]
	return p, ok
}

// Label resolves everything known about an account UUID.
func (r *Resolver) Label(uuid string) Label {
	var l Label
	if r.cfg != nil {
		l.Name = r.cfg.Labels[uuid]
	}
	p, hasProfile := r.profile(uuid)
	if hasProfile {
		l.Email = p.email
	}
	switch {
	case l.Name != "":
		l.Source = "user"
	case r.IsCurrent(uuid):
		l.Source = "current"
	case r.isLastSignedIn(uuid):
		l.Source = "last-signed-in"
	case hasProfile:
		l.Source = "profile"
	}
	if l.Name == "" && hasProfile {
		l.Name = p.name
	}
	return l
}

// Display returns a one-line description such as "work (current)",
// "me@x.com (last signed in)" or "account 0688fb6b…".
func (r *Resolver) Display(a *store.Account) string {
	l := r.Label(a.UUID)
	var s string
	switch {
	case l.Source == "user" && l.Name != "":
		s = l.Name
	case l.Name != "" && l.Email != "":
		s = fmt.Sprintf("%s <%s>", l.Name, l.Email)
	case l.Email != "":
		s = l.Email
	case l.Name != "":
		s = l.Name
	default:
		s = "account " + shortUUID(a.UUID)
	}
	switch {
	case r.IsCurrent(a.UUID):
		s += " (current)"
	case r.isLastSignedIn(a.UUID):
		s += " (last signed in)"
	}
	return s
}

func shortUUID(u string) string {
	if len(u) > 8 {
		u = u[:8]
	}
	return u + "…"
}

// Fingerprint summarises an account's chats for accounts that have no label.
type Fingerprint struct {
	Chats        int
	LastActive   time.Time
	TopProjects  []string // up to 3, by chat count
	RecentTitles []string // up to 3 newest non-empty titles
}

// FingerprintOf computes the Fingerprint of an account.
func FingerprintOf(a *store.Account) Fingerprint {
	fp := Fingerprint{Chats: a.TotalEntries()}
	if ms := a.LastActivity(); ms > 0 {
		fp.LastActive = time.UnixMilli(ms)
	}
	entries := append([]*store.Entry(nil), a.AllEntries()...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Activity() > entries[j].Activity() })

	counts := map[string]int{}
	for _, e := range entries {
		if e.ProjectDir() != "" {
			counts[e.Project()]++
		}
		if e.Title != "" && len(fp.RecentTitles) < 3 {
			fp.RecentTitles = append(fp.RecentTitles, e.Title)
		}
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > 3 {
		names = names[:3]
	}
	fp.TopProjects = names
	return fp
}
