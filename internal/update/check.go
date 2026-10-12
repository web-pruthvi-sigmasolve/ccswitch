// Package update implements ccswitch's optional once-a-day version check and
// the self-update command. The check is the only network use besides the
// update download itself: one HTTPS GET to the GitHub releases API with no
// identifying data other than a User-Agent that names the version.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
)

// Defaults for Options.
const (
	// DefaultRepo is the GitHub repository that is checked.
	DefaultRepo = "web-pruthvi-sigmasolve/ccswitch"
	// DefaultInterval is how often a background check may hit the network.
	DefaultInterval = 24 * time.Hour
	// DefaultTimeout bounds one check request.
	DefaultTimeout = 3 * time.Second
	// DefaultAPIBase is the GitHub API origin.
	DefaultAPIBase = "https://api.github.com"
	// retryAfterFailure is the minimum gap between two attempts.
	retryAfterFailure = time.Hour
	// cacheFileName is the cache file inside Options.ConfigDir.
	cacheFileName = "update-check.json"
	// maxResponseBytes caps the size of the API response we read.
	maxResponseBytes = 1 << 20
)

// Status is the result of a version check.
type Status struct {
	// Current is the running version without a leading "v".
	Current string
	// Latest is the newest release without a leading "v".
	Latest string
	// Available is true when Latest is newer than Current. Pre-release
	// versions are ignored unless Current is a pre-release.
	Available bool
	// CheckedAt is when Latest was fetched.
	CheckedAt time.Time
	// URL is the release page.
	URL string
}

// Options configure a Checker.
type Options struct {
	// ConfigDir holds the cache file update-check.json.
	ConfigDir string
	// Current is the running version (version.Version).
	Current string
	// Repo is "owner/name"; default DefaultRepo.
	Repo string
	// Interval is the minimum age before a check is repeated; default 24h.
	Interval time.Duration
	// Timeout bounds a request; default 3s.
	Timeout time.Duration
	// Getenv is a test hook; default os.Getenv.
	Getenv func(string) string
	// Now is a test hook; default time.Now.
	Now func() time.Time
	// BaseURL is a test hook; default DefaultAPIBase.
	BaseURL string
}

// cacheFile is the on-disk cache (mode 0600).
type cacheFile struct {
	CheckedAt   time.Time `json:"checkedAt"`
	LastAttempt time.Time `json:"lastAttempt"`
	Latest      string    `json:"latest"`
	URL         string    `json:"url"`
}

// release is the part of the GitHub release JSON that we read.
type release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// Checker reads and refreshes the version-check cache.
type Checker struct {
	o       Options
	once    sync.Once
	started bool
	done    chan struct{}
}

// New returns a Checker, filling Options defaults.
func New(o Options) *Checker {
	if o.Repo == "" {
		o.Repo = DefaultRepo
	}
	if o.Interval <= 0 {
		o.Interval = DefaultInterval
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.BaseURL == "" {
		o.BaseURL = DefaultAPIBase
	}
	o.BaseURL = strings.TrimRight(o.BaseURL, "/")
	o.Current = trimV(o.Current)
	return &Checker{o: o, done: make(chan struct{})}
}

// DisabledReason explains why checking is off, or returns "" when it is on.
// The reasons are "dev build", "config", "CCSWITCH_NO_UPDATE_CHECK" and "CI".
func DisabledReason(cfg *config.Config, current string, getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	switch {
	case current == "" || current == "dev":
		return "dev build"
	case cfg != nil && cfg.UpdateCheck == config.UpdateCheckOff:
		return "config"
	case getenv("CCSWITCH_NO_UPDATE_CHECK") != "":
		return "CCSWITCH_NO_UPDATE_CHECK"
	case getenv("CI") != "":
		return "CI"
	}
	return ""
}

// Disabled reports whether checking is off: cfg.UpdateCheck is "off", the
// environment variable CCSWITCH_NO_UPDATE_CHECK or CI is non-empty, or current
// is "dev" or empty. cfg may be nil.
func Disabled(cfg *config.Config, current string, getenv func(string) string) bool {
	return DisabledReason(cfg, current, getenv) != ""
}

func (c *Checker) cachePath() string { return filepath.Join(c.o.ConfigDir, cacheFileName) }

// readCache returns the raw cache; ok is false when it is missing or corrupt.
func (c *Checker) readCache() (cacheFile, bool) {
	data, err := os.ReadFile(c.cachePath())
	if err != nil {
		return cacheFile{}, false
	}
	var cf cacheFile
	if json.Unmarshal(data, &cf) != nil {
		return cacheFile{}, false
	}
	return cf, true
}

// writeCache stores cf atomically with mode 0600. Errors are returned for
// tests; callers ignore them.
func (c *Checker) writeCache(cf cacheFile) error {
	if err := os.MkdirAll(c.o.ConfigDir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := json.Marshal(cf)
	if err != nil {
		return fmt.Errorf("encoding update cache: %w", err)
	}
	tmp, err := os.CreateTemp(c.o.ConfigDir, cacheFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp update cache: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return fmt.Errorf("writing update cache: %w", err)
	}
	_ = tmp.Chmod(0o600) // not supported on Windows; the dir is private anyway
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("closing update cache: %w", err)
	}
	if err := os.Rename(name, c.cachePath()); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("replacing update cache: %w", err)
	}
	return nil
}

// status converts a cache entry to a Status; ok is false when it holds no
// valid version.
func (c *Checker) status(cf cacheFile) (Status, bool) {
	latest := trimV(cf.Latest)
	if _, ok := parseSemver(latest); !ok {
		return Status{}, false
	}
	return Status{
		Current:   c.o.Current,
		Latest:    latest,
		Available: isNewer(latest, c.o.Current),
		CheckedAt: cf.CheckedAt,
		URL:       cf.URL,
	}, true
}

// Cached returns the last stored status if the cache file exists and is
// valid, whatever its age.
func (c *Checker) Cached() (Status, bool) {
	cf, ok := c.readCache()
	if !ok {
		return Status{}, false
	}
	return c.status(cf)
}

// Stale reports whether the cache is missing or older than Interval.
func (c *Checker) Stale() bool {
	cf, ok := c.readCache()
	if !ok || cf.CheckedAt.IsZero() {
		return true
	}
	return c.o.Now().Sub(cf.CheckedAt) >= c.o.Interval
}

// shouldFetch is Stale plus the rule that a failed or interrupted attempt is
// not repeated within an hour.
func (c *Checker) shouldFetch() bool {
	if !c.Stale() {
		return false
	}
	if cf, ok := c.readCache(); ok && !cf.LastAttempt.IsZero() &&
		c.o.Now().Sub(cf.LastAttempt) < retryAfterFailure {
		return false
	}
	return true
}

// Start begins a background refresh if the cache is stale. It is safe to call
// more than once (only the first call acts) and never blocks on the network.
// It does nothing when checking is disabled by the environment or a dev build.
func (c *Checker) Start() {
	c.once.Do(func() {
		if DisabledReason(nil, c.o.Current, c.o.Getenv) != "" || !c.shouldFetch() {
			close(c.done)
			return
		}
		c.started = true
		// Record the attempt first so that a process that exits before the
		// request finishes does not retry on every run.
		c.markAttempt()
		go func() {
			defer close(c.done)
			_, _ = c.refresh(context.Background())
		}()
	})
}

// Result waits up to wait for a refresh begun by Start and returns the newest
// status known: the fresh one when the refresh finished, else the cached one.
// ok is false when nothing is known. It never returns an error.
func (c *Checker) Result(wait time.Duration) (Status, bool) {
	if c.started && wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-c.done:
		case <-t.C:
		}
		t.Stop()
	}
	return c.Cached()
}

// Refresh checks GitHub now, ignoring the interval and the retry limit, and
// stores the result. Use it for an explicit `ccswitch update`; unlike Start it
// does not honour Disabled. On failure the cache keeps its last good value.
func (c *Checker) Refresh(ctx context.Context) (Status, error) {
	return c.refresh(ctx)
}

// markAttempt stores the attempt time and keeps the rest of the cache.
func (c *Checker) markAttempt() {
	cf, _ := c.readCache()
	cf.LastAttempt = c.o.Now()
	_ = c.writeCache(cf)
}

func (c *Checker) refresh(ctx context.Context) (Status, error) {
	rel, err := c.fetch(ctx)
	if err != nil {
		c.markAttempt()
		return Status{}, err
	}
	now := c.o.Now()
	cf := cacheFile{CheckedAt: now, LastAttempt: now, Latest: trimV(rel.TagName), URL: rel.HTMLURL}
	_ = c.writeCache(cf) // the answer is still good if only the cache write fails
	s, _ := c.status(cf)
	return s, nil
}

// fetch performs the single GET request.
func (c *Checker) fetch(ctx context.Context) (release, error) {
	ctx, cancel := context.WithTimeout(ctx, c.o.Timeout)
	defer cancel()
	url := c.o.BaseURL + "/repos/" + c.o.Repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return release{}, fmt.Errorf("building update request: %w", err)
	}
	req.Header.Set("User-Agent", "ccswitch/"+c.o.Current)
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{
		Timeout: c.o.Timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 3 || r.URL.Host != via[0].URL.Host {
				return errors.New("unexpected redirect")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("update check request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("update check: GitHub answered %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&rel); err != nil {
		return release{}, fmt.Errorf("parsing update check response: %w", err)
	}
	if _, ok := parseSemver(rel.TagName); !ok {
		return release{}, fmt.Errorf("update check: unrecognised release tag %q", rel.TagName)
	}
	return rel, nil
}

// Notice formats the one-line message for an available update, or "" when
// there is none.
func Notice(s Status) string {
	if !s.Available {
		return ""
	}
	return fmt.Sprintf("ccswitch %s is available (you have %s). Run: ccswitch update", s.Latest, s.Current)
}
