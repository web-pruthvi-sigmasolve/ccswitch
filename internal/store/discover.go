package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ErrStoreNotFound is returned (wrapped, with the checked paths in the message)
// when no candidate user-data folder holds a claude-code-sessions store. The
// returned *Store is still non-nil so callers such as `doctor` can report the
// candidates.
var ErrStoreNotFound = errors.New("claude-code-sessions store not found")

// Options configures Discover. The zero value discovers using the real
// environment.
type Options struct {
	// UserDataOverride, when set, is the only candidate examined.
	UserDataOverride string
	// TranscriptRootOverride replaces the Claude Code projects folder.
	TranscriptRootOverride string
	// HomeDir is a test hook; default os.UserHomeDir.
	HomeDir string
	// Getenv is a test hook; default os.Getenv.
	Getenv func(string) string
	// GOOS is a test hook; default runtime.GOOS.
	GOOS string
}

const sessionsDirName = "claude-code-sessions"

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsUUID reports whether s is a lowercase hex 8-4-4-4-12 UUID.
func IsUUID(s string) bool { return uuidRe.MatchString(s) }

// storeScore summarises one candidate user-data folder.
type storeScore struct {
	dirExists   bool
	hasSessions bool
	entries     int
	buckets     int
	newest      time.Time
}

func (s storeScore) valid() bool { return s.buckets > 0 }

// Discover locates the Claude Desktop user-data folder, scores every candidate
// and loads the chosen session store. When nothing is found it returns a
// non-nil Store (UserData/Root/Candidates filled in) together with an error
// wrapping ErrStoreNotFound.
func Discover(opts Options) (*Store, error) {
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	home := opts.HomeDir
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	transcriptRoot := opts.TranscriptRootOverride
	if transcriptRoot == "" {
		if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
			transcriptRoot = filepath.Join(d, "projects")
		} else if home != "" {
			transcriptRoot = filepath.Join(home, ".claude", "projects")
		}
	}

	var cands []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			cands = append(cands, p)
		}
	}
	explicit := opts.UserDataOverride != ""
	if explicit {
		add(opts.UserDataOverride)
	} else {
		for _, p := range knownCandidates(goos, getenv, home) {
			add(p)
		}
	}

	scores := map[string]storeScore{}
	anyValid := false
	for _, c := range cands {
		sc := scoreStore(c)
		scores[c] = sc
		anyValid = anyValid || sc.valid()
	}
	if !explicit && !anyValid {
		for _, p := range scanCandidates(goos, getenv, home) {
			if !seen[filepath.Clean(p)] {
				add(p)
				sc := scoreStore(filepath.Clean(p))
				scores[filepath.Clean(p)] = sc
				anyValid = anyValid || sc.valid()
			}
		}
	}

	best := ""
	for _, c := range cands {
		sc := scores[c]
		if !sc.valid() {
			continue
		}
		if best == "" || better(sc, scores[best]) {
			best = c
		}
	}

	st := &Store{TranscriptRoot: transcriptRoot}
	for _, c := range cands {
		sc := scores[c]
		cand := Candidate{Path: c, Entries: sc.entries, Buckets: sc.buckets, Newest: sc.newest, Chosen: c == best && best != ""}
		cand.Reason = reason(c, sc, best, scores)
		st.Candidates = append(st.Candidates, cand)
	}

	if best == "" {
		switch {
		case len(cands) > 0:
			st.UserData = cands[0]
		case home != "":
			st.UserData = defaultUserData(goos, home)
		}
		st.Root = filepath.Join(st.UserData, sessionsDirName)
		checked := make([]string, 0, len(cands))
		for _, c := range cands {
			checked = append(checked, filepath.Join(c, sessionsDirName))
		}
		return st, fmt.Errorf("%w: checked %s", ErrStoreNotFound, strings.Join(checked, ", "))
	}

	st.UserData = best
	st.Root = filepath.Join(best, sessionsDirName)
	accounts, err := loadAccounts(st.Root, transcriptRoot)
	if err != nil {
		return st, fmt.Errorf("reading session store %s: %w", st.Root, err)
	}
	st.Accounts = accounts
	return st, nil
}

func defaultUserData(goos, home string) string {
	switch goos {
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Claude")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude")
	default:
		return filepath.Join(home, ".config", "Claude")
	}
}

// better reports whether a beats b: more entries, then more buckets, then newer.
func better(a, b storeScore) bool {
	if a.entries != b.entries {
		return a.entries > b.entries
	}
	if a.buckets != b.buckets {
		return a.buckets > b.buckets
	}
	return a.newest.After(b.newest)
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func reason(path string, sc storeScore, best string, scores map[string]storeScore) string {
	switch {
	case !sc.dirExists:
		return "folder does not exist"
	case !sc.hasSessions:
		return "no claude-code-sessions folder"
	case !sc.valid():
		return "claude-code-sessions holds no chats"
	case path == best:
		return fmt.Sprintf("chosen: %s in %s", plural(sc.entries, "chat"), plural(sc.buckets, "bucket"))
	}
	b := scores[best]
	switch {
	case sc.entries < b.entries:
		return fmt.Sprintf("fewer chats than %s (%d < %d)", best, sc.entries, b.entries)
	case sc.buckets < b.buckets:
		return fmt.Sprintf("fewer buckets than %s (%d < %d)", best, sc.buckets, b.buckets)
	default:
		return fmt.Sprintf("not newer than %s", best)
	}
}

// knownCandidates lists the fixed-shape locations for the OS, in priority order.
func knownCandidates(goos string, getenv func(string) string, home string) []string {
	var out []string
	add := func(p ...string) { out = append(out, p...) }
	add(getenv("CLAUDE_USER_DATA"))
	switch goos {
	case "windows":
		if v := getenv("APPDATA"); v != "" {
			add(filepath.Join(v, "Claude"))
		} else if home != "" {
			add(filepath.Join(home, "AppData", "Roaming", "Claude"))
		}
		local := getenv("LOCALAPPDATA")
		if local == "" && home != "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		if local != "" {
			add(filepath.Join(local, "Claude"))
			add(expand(local, "Packages", "*Claude*", "LocalCache", "Roaming", "Claude")...)
		}
	case "darwin":
		if home != "" {
			add(filepath.Join(home, "Library", "Application Support", "Claude"))
			add(expand(home, "Library", "Containers", "*", "Data", "Library", "Application Support", "Claude")...)
			add(expand(home, "Library", "Group Containers", "*", "Library", "Application Support", "Claude")...)
		}
	default:
		if home != "" {
			add(filepath.Join(home, ".config", "Claude"))
		}
		if x := getenv("XDG_CONFIG_HOME"); x != "" {
			add(filepath.Join(x, "Claude"))
		}
		if home != "" {
			add(expand(home, ".var", "app", "*", "config", "Claude")...)
			add(expand(home, "snap", "*", "current", ".config", "Claude")...)
		}
	}
	return out
}

// expand joins base with segs, expanding segments that contain '*' against the
// directory contents. Only existing directories are returned for wildcard
// patterns; literal paths are returned as-is. Base is never treated as a pattern.
func expand(base string, segs ...string) []string {
	cur := []string{base}
	for _, seg := range segs {
		var next []string
		for _, c := range cur {
			if !strings.ContainsAny(seg, "*?[") {
				next = append(next, filepath.Join(c, seg))
				continue
			}
			ents, err := os.ReadDir(c)
			if err != nil {
				continue
			}
			for _, e := range ents {
				if ok, _ := filepath.Match(seg, e.Name()); ok {
					next = append(next, filepath.Join(c, e.Name()))
				}
			}
		}
		cur = next
	}
	return cur
}

// scanCandidates is the last-resort bounded recursive search for
// claude-code-sessions folders; it returns their parents.
func scanCandidates(goos string, getenv func(string) string, home string) []string {
	type spec struct {
		root  string
		depth int
	}
	var specs []spec
	switch goos {
	case "darwin":
		if home != "" {
			specs = []spec{{filepath.Join(home, "Library", "Application Support"), 4}, {filepath.Join(home, "Library", "Containers"), 6}}
		}
	case "windows":
		if v := getenv("APPDATA"); v != "" {
			specs = append(specs, spec{v, 3})
		}
		if v := getenv("LOCALAPPDATA"); v != "" {
			specs = append(specs, spec{filepath.Join(v, "Packages"), 6})
		}
	default:
		if home != "" {
			specs = []spec{{filepath.Join(home, ".config"), 4}, {filepath.Join(home, ".var", "app"), 5}, {filepath.Join(home, "snap"), 6}}
		}
	}
	var out []string
	for _, sp := range specs {
		if fi, err := os.Stat(sp.root); err != nil || !fi.IsDir() {
			continue
		}
		_ = filepath.WalkDir(sp.root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if !d.IsDir() || p == sp.root {
				return nil
			}
			rel, rerr := filepath.Rel(sp.root, p)
			if rerr != nil {
				return nil
			}
			depth := strings.Count(rel, string(filepath.Separator)) + 1
			if d.Name() == sessionsDirName {
				out = append(out, filepath.Dir(p))
				return fs.SkipDir
			}
			if depth >= sp.depth {
				return fs.SkipDir
			}
			return nil
		})
	}
	return out
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// scoreStore counts chats and buckets without reading any file content.
func scoreStore(userData string) storeScore {
	var sc storeScore
	sc.dirExists = isDir(userData)
	root := filepath.Join(userData, sessionsDirName)
	if !isDir(root) {
		return sc
	}
	sc.hasSessions = true
	accs, err := os.ReadDir(root)
	if err != nil {
		return sc
	}
	for _, a := range accs {
		ap := filepath.Join(root, a.Name())
		if !IsUUID(a.Name()) || !isDir(ap) {
			continue
		}
		orgs, err := os.ReadDir(ap)
		if err != nil {
			continue
		}
		for _, o := range orgs {
			op := filepath.Join(ap, o.Name())
			if !IsUUID(o.Name()) || !isDir(op) {
				continue
			}
			files, err := os.ReadDir(op)
			if err != nil {
				continue
			}
			n := 0
			for _, f := range files {
				if !isSidebarName(f.Name()) || f.IsDir() {
					continue
				}
				n++
				if info, err := f.Info(); err == nil && info.ModTime().After(sc.newest) {
					sc.newest = info.ModTime()
				}
			}
			if n > 0 {
				sc.buckets++
				sc.entries += n
			}
		}
	}
	return sc
}

func isSidebarName(name string) bool {
	return strings.HasPrefix(name, "local_") && strings.HasSuffix(name, ".json")
}

// loadAccounts reads every account/org folder under root and populates entries.
func loadAccounts(root, transcriptRoot string) ([]*Account, error) {
	accs, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []*Account
	for _, a := range accs {
		ap := filepath.Join(root, a.Name())
		if !IsUUID(a.Name()) || !isDir(ap) {
			continue
		}
		acc := &Account{UUID: a.Name(), Path: ap}
		orgs, err := os.ReadDir(ap)
		if err != nil {
			return nil, err
		}
		for _, o := range orgs {
			op := filepath.Join(ap, o.Name())
			if !IsUUID(o.Name()) || !isDir(op) {
				continue
			}
			b := &Bucket{AccountUUID: acc.UUID, OrgUUID: o.Name(), Path: op}
			ReadBucket(b, transcriptRoot)
			acc.Buckets = append(acc.Buckets, b)
		}
		sort.SliceStable(acc.Buckets, func(i, j int) bool {
			ai, aj := acc.Buckets[i].LastActivity(), acc.Buckets[j].LastActivity()
			if ai != aj {
				return ai > aj
			}
			return acc.Buckets[i].OrgUUID < acc.Buckets[j].OrgUUID
		})
		out = append(out, acc)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].LastActivity(), out[j].LastActivity()
		if ai != aj {
			return ai > aj
		}
		return out[i].UUID < out[j].UUID
	})
	return out, nil
}
