package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
)

var testNow = time.Date(2026, 10, 13, 9, 12, 0, 0, time.UTC)

func noEnv(string) string { return "" }

// fakeAPI serves a releases/latest answer and counts requests.
func fakeAPI(t *testing.T, status int, tag string) (*httptest.Server, *atomic.Int32, *http.Header) {
	t.Helper()
	var n atomic.Int32
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		hdr = r.Header.Clone()
		if r.URL.Path != "/repos/"+DefaultRepo+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": tag, "html_url": "https://example.test/release", "name": "ignored",
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &n, &hdr
}

func newChecker(dir, base string, now time.Time) *Checker {
	return New(Options{ConfigDir: dir, Current: "0.1.1", BaseURL: base, Getenv: noEnv, Now: func() time.Time { return now }})
}

func writeCacheFile(t *testing.T, dir string, cf cacheFile) {
	t.Helper()
	data, err := json.Marshal(cf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cacheFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStartFetchesWhenStale(t *testing.T) {
	srv, n, hdr := fakeAPI(t, http.StatusOK, "v0.2.0")
	dir := t.TempDir()
	c := newChecker(dir, srv.URL, testNow)
	if !c.Stale() {
		t.Fatal("empty cache should be stale")
	}
	if _, ok := c.Cached(); ok {
		t.Fatal("Cached should be empty")
	}
	c.Start()
	c.Start() // second call must be harmless
	s, ok := c.Result(5 * time.Second)
	if !ok {
		t.Fatal("Result returned nothing")
	}
	if s.Latest != "0.2.0" || s.Current != "0.1.1" || !s.Available || s.URL != "https://example.test/release" || !s.CheckedAt.Equal(testNow) {
		t.Errorf("status = %+v", s)
	}
	if n.Load() != 1 {
		t.Errorf("requests = %d, want 1", n.Load())
	}
	if got := hdr.Get("User-Agent"); got != "ccswitch/0.1.1" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := hdr.Get("Accept"); got != "application/vnd.github+json" {
		t.Errorf("Accept = %q", got)
	}
	for k := range *hdr {
		switch k {
		case "User-Agent", "Accept", "Accept-Encoding":
		default:
			t.Errorf("unexpected request header %q", k)
		}
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, cacheFileName))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("cache mode = %v", fi.Mode().Perm())
		}
	}
	if c.Stale() {
		t.Error("cache should be fresh now")
	}
}

func TestStartSkipsWhenFresh(t *testing.T) {
	srv, n, _ := fakeAPI(t, http.StatusOK, "v9.9.9")
	dir := t.TempDir()
	writeCacheFile(t, dir, cacheFile{CheckedAt: testNow.Add(-time.Hour), Latest: "0.1.1", URL: "u"})
	c := newChecker(dir, srv.URL, testNow)
	if c.Stale() {
		t.Fatal("cache from 1h ago should not be stale")
	}
	c.Start()
	s, ok := c.Result(time.Second)
	if !ok || s.Latest != "0.1.1" || s.Available {
		t.Errorf("status = %+v ok=%v", s, ok)
	}
	if n.Load() != 0 {
		t.Errorf("requests = %d, want 0", n.Load())
	}
}

func TestStartRefetchesOldCache(t *testing.T) {
	srv, n, _ := fakeAPI(t, http.StatusOK, "0.3.0")
	dir := t.TempDir()
	writeCacheFile(t, dir, cacheFile{CheckedAt: testNow.Add(-25 * time.Hour), Latest: "0.1.1"})
	c := newChecker(dir, srv.URL, testNow)
	c.Start()
	s, _ := c.Result(5 * time.Second)
	if s.Latest != "0.3.0" || n.Load() != 1 {
		t.Errorf("latest %q, requests %d", s.Latest, n.Load())
	}
}

func TestFailureKeepsCacheAndLimitsRetries(t *testing.T) {
	srv, n, _ := fakeAPI(t, http.StatusInternalServerError, "")
	dir := t.TempDir()
	old := cacheFile{CheckedAt: testNow.Add(-48 * time.Hour), Latest: "0.1.5", URL: "old"}
	writeCacheFile(t, dir, old)

	c := newChecker(dir, srv.URL, testNow)
	c.Start()
	s, ok := c.Result(5 * time.Second)
	if !ok || s.Latest != "0.1.5" || !s.CheckedAt.Equal(old.CheckedAt) || s.URL != "old" {
		t.Errorf("cached status changed: %+v ok=%v", s, ok)
	}
	if n.Load() != 1 {
		t.Fatalf("requests = %d, want 1", n.Load())
	}
	cf, _ := c.readCache()
	if !cf.LastAttempt.Equal(testNow) || cf.Latest != "0.1.5" {
		t.Errorf("cache = %+v", cf)
	}

	// 30 minutes later: still stale, but no new attempt.
	c2 := newChecker(dir, srv.URL, testNow.Add(30*time.Minute))
	c2.Start()
	c2.Result(time.Second)
	if n.Load() != 1 {
		t.Errorf("requests after retry window = %d, want 1", n.Load())
	}

	// 2 hours later: allowed again.
	c3 := newChecker(dir, srv.URL, testNow.Add(2*time.Hour))
	c3.Start()
	c3.Result(5 * time.Second)
	if n.Load() != 2 {
		t.Errorf("requests after an hour = %d, want 2", n.Load())
	}
}

func TestFailureWithoutCacheKnowsNothing(t *testing.T) {
	srv, _, _ := fakeAPI(t, http.StatusInternalServerError, "")
	dir := t.TempDir()
	c := newChecker(dir, srv.URL, testNow)
	c.Start()
	if s, ok := c.Result(5 * time.Second); ok {
		t.Errorf("unexpected status %+v", s)
	}
	cf, ok := c.readCache()
	if !ok || cf.LastAttempt.IsZero() || cf.Latest != "" {
		t.Errorf("cache = %+v ok=%v", cf, ok)
	}
}

func TestBadResponses(t *testing.T) {
	for _, tag := range []string{"", "latest", "1.2"} {
		srv, _, _ := fakeAPI(t, http.StatusOK, tag)
		c := newChecker(t.TempDir(), srv.URL, testNow)
		if _, err := c.Refresh(context.Background()); err == nil {
			t.Errorf("tag %q: expected an error", tag)
		}
	}
}

func TestRefreshIgnoresInterval(t *testing.T) {
	srv, n, _ := fakeAPI(t, http.StatusOK, "v0.2.0")
	dir := t.TempDir()
	writeCacheFile(t, dir, cacheFile{CheckedAt: testNow, LastAttempt: testNow, Latest: "0.1.1"})
	c := newChecker(dir, srv.URL, testNow)
	s, err := c.Refresh(context.Background())
	if err != nil || s.Latest != "0.2.0" || !s.Available || n.Load() != 1 {
		t.Errorf("status %+v err %v requests %d", s, err, n.Load())
	}
}

func TestTimeout(t *testing.T) {
	// The handler blocks until the test ends. Cleanups run last-in first-out,
	// so the channel is closed before srv.Close, which waits for handlers.
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	newSlow := func() *Checker {
		return New(Options{ConfigDir: t.TempDir(), Current: "0.1.1", BaseURL: srv.URL, Getenv: noEnv, Timeout: 200 * time.Millisecond})
	}

	// Explicit refresh.
	start := time.Now()
	if _, err := newSlow().Refresh(context.Background()); err == nil {
		t.Error("expected a timeout error")
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("Refresh: timeout not honoured, took %v", d)
	}

	// Background check: Result must give up on the timeout, long before its
	// own 2s wait, and report nothing because no answer arrived.
	c := newSlow()
	start = time.Now()
	c.Start()
	if s, ok := c.Result(2 * time.Second); ok {
		t.Errorf("unexpected status %+v", s)
	}
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("Start/Result: timeout not honoured, took %v", d)
	}
}

func TestStartNoopWhenDisabledByEnv(t *testing.T) {
	srv, n, _ := fakeAPI(t, http.StatusOK, "v0.2.0")
	c := New(Options{
		ConfigDir: t.TempDir(), Current: "0.1.1", BaseURL: srv.URL,
		Getenv: func(k string) string {
			if k == "CCSWITCH_NO_UPDATE_CHECK" {
				return "1"
			}
			return ""
		},
	})
	c.Start()
	if _, ok := c.Result(time.Second); ok || n.Load() != 0 {
		t.Errorf("disabled checker fetched (requests %d)", n.Load())
	}
}

func TestDisabled(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	off := &config.Config{UpdateCheck: "off"}
	auto := &config.Config{UpdateCheck: "auto"}
	tests := []struct {
		name    string
		cfg     *config.Config
		current string
		env     map[string]string
		want    string
	}{
		{"enabled nil cfg", nil, "0.1.1", nil, ""},
		{"enabled auto", auto, "0.1.1", nil, ""},
		{"enabled empty", &config.Config{}, "0.1.1", nil, ""},
		{"config off", off, "0.1.1", nil, "config"},
		{"env opt-out", auto, "0.1.1", map[string]string{"CCSWITCH_NO_UPDATE_CHECK": "1"}, "CCSWITCH_NO_UPDATE_CHECK"},
		{"CI", auto, "0.1.1", map[string]string{"CI": "true"}, "CI"},
		{"dev build", auto, "dev", nil, "dev build"},
		{"empty version", auto, "", nil, "dev build"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DisabledReason(tc.cfg, tc.current, env(tc.env)); got != tc.want {
				t.Errorf("DisabledReason = %q, want %q", got, tc.want)
			}
			if got := Disabled(tc.cfg, tc.current, env(tc.env)); got != (tc.want != "") {
				t.Errorf("Disabled = %v", got)
			}
		})
	}
}

func TestNotice(t *testing.T) {
	got := Notice(Status{Current: "0.1.1", Latest: "0.2.0", Available: true})
	want := "ccswitch 0.2.0 is available (you have 0.1.1). Run: ccswitch update"
	if got != want {
		t.Errorf("Notice = %q", got)
	}
	if Notice(Status{Current: "0.1.1", Latest: "0.1.1"}) != "" {
		t.Error("Notice should be empty when nothing is available")
	}
}

func TestCachedIgnoresCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, cacheFileName), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newChecker(dir, "http://127.0.0.1:1", testNow)
	if _, ok := c.Cached(); ok || !c.Stale() {
		t.Error("corrupt cache should be treated as missing")
	}
	writeCacheFile(t, dir, cacheFile{CheckedAt: testNow, Latest: "garbage"})
	if _, ok := c.Cached(); ok {
		t.Error("invalid version should not be returned")
	}
}

func TestVersionNormalisation(t *testing.T) {
	c := New(Options{ConfigDir: t.TempDir(), Current: "v0.1.1"})
	if c.o.Current != "0.1.1" || !strings.HasPrefix(c.o.BaseURL, "https://") {
		t.Errorf("options = %+v", c.o)
	}
}

func TestSemverCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.1.0", -1},
		{"1.1.0", "2.0.0", -1},
		{"0.9.0", "0.10.0", -1},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0-rc1", "1.0.0-rc2", -1},
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"1.0.0-rc.2", "1.0.0-rc.10", -1},
		{"1.2.3", "v1.2.3", 0},
		{"1.2.3+build", "1.2.3", 0},
		{"2.0.0", "1.9.9", 1},
	}
	for _, tc := range tests {
		a, ok1 := parseSemver(tc.a)
		b, ok2 := parseSemver(tc.b)
		if !ok1 || !ok2 {
			t.Fatalf("parse failed for %q / %q", tc.a, tc.b)
		}
		if got := a.compare(b); got != tc.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := b.compare(a); got != -tc.want {
			t.Errorf("compare(%q, %q) = %d, want %d", tc.b, tc.a, got, -tc.want)
		}
	}
	for _, bad := range []string{"", "dev", "1.2", "1.2.x", "1.2.3.4", "1.2.3-", "-1.2.3", "1..3", "+1.2.3"} {
		if _, ok := parseSemver(bad); ok {
			t.Errorf("parseSemver(%q) should fail", bad)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"0.2.0", "0.1.1", true},
		{"0.1.1", "0.1.1", false},
		{"0.1.0", "0.1.1", false},
		{"0.2.0-rc1", "0.1.1", false},
		{"0.2.0-rc1", "0.2.0-beta1", true},
		{"0.2.0", "0.2.0-rc1", true},
		{"0.2.0", "dev", false},
		{"junk", "0.1.1", false},
	}
	for _, tc := range tests {
		if got := isNewer(tc.latest, tc.current); got != tc.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}
