package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// updateHarness is a CLI environment with a fake GitHub.
type updateHarness struct {
	t       *testing.T
	cfgDir  string
	exe     string
	api     *httptest.Server
	apiHits atomic.Int32
	dlHits  atomic.Int32
	out     bytes.Buffer
	errb    bytes.Buffer
	e       *env
}

// newUpdateHarness serves tag as the latest release and a downloadable archive
// that contains newBinary for the running OS and architecture (tar.gz).
func newUpdateHarness(t *testing.T, tag, newBinary string) *updateHarness {
	t.Helper()
	h := &updateHarness{t: t, cfgDir: t.TempDir()}
	h.exe = filepath.Join(t.TempDir(), "ccswitch")
	if err := os.WriteFile(h.exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "ccswitch", Mode: 0o755, Size: int64(len(newBinary)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(newBinary))
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	ver := strings.TrimPrefix(tag, "v")
	archive := fmt.Sprintf("ccswitch_%s_linux_%s.tar.gz", ver, runtime.GOARCH)

	h.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			h.apiHits.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag, "html_url": "https://example.test/r"})
		case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
			h.dlHits.Add(1)
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), archive)
		case strings.HasSuffix(r.URL.Path, "/"+archive):
			h.dlHits.Add(1)
			_, _ = w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.api.Close)

	h.e = newEnv(strings.NewReader(""), &h.out, &h.errb)
	h.e.version = "0.1.1"
	h.e.goos = "linux"
	h.e.getenv = func(string) string { return "" }
	h.e.updateAPI = h.api.URL
	h.e.updateDL = h.api.URL
	h.e.exePath = func() (string, error) { return h.exe, nil }
	return h
}

func (h *updateHarness) run(args ...string) int {
	h.t.Helper()
	h.out.Reset()
	h.errb.Reset()
	return h.e.run(append([]string{"--config-dir", h.cfgDir}, args...))
}

func TestUpdateCheck(t *testing.T) {
	t.Run("available", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		if code := h.run("update", "--check"); code != 0 {
			t.Fatalf("code %d: %s", code, h.errb.String())
		}
		contains(t, h.out.String(), "ccswitch 0.2.0 is available (you have 0.1.1). Run: ccswitch update")
	})
	t.Run("up to date", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.1.1", "NEW")
		if code := h.run("update", "--check"); code != 0 {
			t.Fatalf("code %d", code)
		}
		contains(t, h.out.String(), "You are on the latest version (0.1.1).")
	})
	t.Run("json", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		if code := h.run("update", "--check", "--json"); code != 0 {
			t.Fatalf("code %d", code)
		}
		var got updateJSON
		if err := json.Unmarshal(h.out.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := updateJSON{Current: "0.1.1", Latest: "0.2.0", Available: true, URL: "https://example.test/r"}
		if got != want {
			t.Errorf("json = %+v, want %+v", got, want)
		}
	})
	t.Run("network failure", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		h.e.updateAPI = "http://127.0.0.1:1"
		if code := h.run("update", "--check"); code != exitUser {
			t.Fatalf("code %d", code)
		}
		contains(t, h.errb.String(), "ccswitch: Could not check for updates.", "hint:")
	})
	t.Run("dev build", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		h.e.version = "dev"
		if code := h.run("update"); code != 0 {
			t.Fatalf("code %d", code)
		}
		contains(t, h.out.String(), "development build")
		if h.apiHits.Load() != 0 {
			t.Error("a dev build must not hit the network")
		}
	})
}

func TestUpdateInstalls(t *testing.T) {
	t.Run("yes replaces the binary", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		if code := h.run("update", "--yes"); code != 0 {
			t.Fatalf("code %d: %s", code, h.errb.String())
		}
		contains(t, h.out.String(), "Updated to 0.2.0.")
		if got, _ := os.ReadFile(h.exe); string(got) != "NEW" {
			t.Errorf("exe = %q", got)
		}
	})
	t.Run("up to date does nothing", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.1.1", "NEW")
		if code := h.run("update", "--yes"); code != 0 {
			t.Fatalf("code %d", code)
		}
		contains(t, h.out.String(), "You are on the latest version (0.1.1).")
		if h.dlHits.Load() != 0 {
			t.Error("nothing should be downloaded")
		}
	})
	t.Run("explicit version", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.3.0", "NEW")
		if code := h.run("update", "--yes", "--version", "v0.3.0"); code != 0 {
			t.Fatalf("code %d: %s", code, h.errb.String())
		}
		if h.apiHits.Load() != 0 {
			t.Error("--version must not query the latest release")
		}
		if got, _ := os.ReadFile(h.exe); string(got) != "NEW" {
			t.Errorf("exe = %q", got)
		}
	})
	t.Run("bad version", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		if code := h.run("update", "--version", "banana"); code != exitUser {
			t.Fatalf("code %d", code)
		}
	})
	t.Run("refuses without a terminal", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		if code := h.run("update"); code != exitUser {
			t.Fatalf("code %d", code)
		}
		contains(t, h.errb.String(), "without confirmation", "--yes")
		if got, _ := os.ReadFile(h.exe); string(got) != "OLD" {
			t.Errorf("exe changed: %q", got)
		}
	})
	t.Run("declined", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		h.e.stdinTTY = true
		h.e.in = strings.NewReader("n\n")
		if code := h.run("update"); code != exitNothing {
			t.Fatalf("code %d", code)
		}
		contains(t, h.out.String(), "Update ccswitch 0.1.1 -> 0.2.0? [y/N]")
		if got, _ := os.ReadFile(h.exe); string(got) != "OLD" {
			t.Errorf("exe changed: %q", got)
		}
	})
	t.Run("accepted", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		h.e.stdinTTY = true
		h.e.in = strings.NewReader("y\n")
		if code := h.run("update"); code != 0 {
			t.Fatalf("code %d: %s", code, h.errb.String())
		}
		if got, _ := os.ReadFile(h.exe); string(got) != "NEW" {
			t.Errorf("exe = %q", got)
		}
	})
	t.Run("package manager prints the hint", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		h.exe = "/opt/homebrew/Cellar/ccswitch/0.1.1/bin/ccswitch"
		if code := h.run("update", "--yes"); code != 0 {
			t.Fatalf("code %d: %s", code, h.errb.String())
		}
		contains(t, h.out.String(), "Homebrew", "brew upgrade ccswitch")
		if h.dlHits.Load() != 0 {
			t.Error("a package-managed install must not be replaced")
		}
	})
	t.Run("bad checksum leaves the binary", func(t *testing.T) {
		h := newUpdateHarness(t, "v0.2.0", "NEW")
		h.api.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/releases/latest"):
				_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": "v0.2.0"})
			case strings.HasSuffix(r.URL.Path, "/checksums.txt"):
				fmt.Fprintf(w, "%s  ccswitch_0.2.0_linux_%s.tar.gz\n", strings.Repeat("0", 64), runtime.GOARCH)
			default:
				_, _ = w.Write([]byte("tampered"))
			}
		})
		if code := h.run("update", "--yes"); code != exitUser {
			t.Fatalf("code %d", code)
		}
		contains(t, h.errb.String(), "The update failed", "checksum mismatch", "hint:")
		if got, _ := os.ReadFile(h.exe); string(got) != "OLD" {
			t.Errorf("exe changed: %q", got)
		}
	})
}

func TestUpdateEnableDisable(t *testing.T) {
	h := newUpdateHarness(t, "v0.2.0", "NEW")
	if code := h.run("update", "--disable-check"); code != 0 {
		t.Fatalf("code %d: %s", code, h.errb.String())
	}
	contains(t, h.out.String(), "Update check is now off.")
	cfg, err := config.Load(h.cfgDir)
	if err != nil || cfg.UpdateCheck != "off" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	if code := h.run("update", "--enable-check"); code != 0 {
		t.Fatalf("code %d", code)
	}
	cfg, _ = config.Load(h.cfgDir)
	if cfg.UpdateCheck != "auto" {
		t.Errorf("UpdateCheck = %q", cfg.UpdateCheck)
	}
	if code := h.run("update", "--enable-check", "--disable-check"); code != exitUser {
		t.Errorf("conflicting flags: code %d", code)
	}
	if h.apiHits.Load() != 0 {
		t.Error("changing the setting must not use the network")
	}
}

// writeUpdateCache stores a fresh cache so no request is needed.
func writeUpdateCache(t *testing.T, dir, latest string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"checkedAt": time.Now(), "lastAttempt": time.Now(), "latest": latest, "url": "u"})
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "update-check.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestEndOfCommandNotice(t *testing.T) {
	const notice = "ccswitch 0.2.0 is available (you have 0.1.1). Run: ccswitch update"
	tests := []struct {
		name string
		prep func(h *updateHarness)
		args []string
		want bool
	}{
		{"shown on a terminal", func(h *updateHarness) {}, []string{"doctor"}, true},
		{"not when stderr is not a terminal", func(h *updateHarness) { h.e.stderrTTY = false }, []string{"doctor"}, false},
		{"not with --no-update-check", func(h *updateHarness) {}, []string{"doctor", "--no-update-check"}, false},
		{"not with --quiet", func(h *updateHarness) {}, []string{"doctor", "--quiet"}, false},
		{"not with --json", func(h *updateHarness) {}, []string{"list", "--json"}, false},
		{"not for version", func(h *updateHarness) {}, []string{"version"}, false},
		{"not for update", func(h *updateHarness) {}, []string{"update", "--enable-check"}, false},
		{"not in CI", func(h *updateHarness) {
			h.e.getenv = func(k string) string {
				if k == "CI" {
					return "true"
				}
				return ""
			}
		}, []string{"doctor"}, false},
		{"not when disabled in config", func(h *updateHarness) {
			if err := (&config.Config{UpdateCheck: "off"}).Save(h.cfgDir); err != nil {
				h.t.Fatal(err)
			}
		}, []string{"doctor"}, false},
		{"not for a dev build", func(h *updateHarness) { h.e.version = "dev" }, []string{"doctor"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := setup(t)
			h := newUpdateHarness(t, "v0.2.0", "NEW")
			h.cfgDir = st.cfgDir
			writeUpdateCache(t, h.cfgDir, "0.2.0")
			h.e.stderrTTY = true
			tc.prep(h)
			args := append([]string{"--user-data", st.fx.UserData}, tc.args...)
			if code := h.run(args...); code != 0 {
				t.Fatalf("code %d: %s", code, h.errb.String())
			}
			got := strings.Count(h.errb.String(), notice)
			if tc.want && got != 1 {
				t.Errorf("notice printed %d times, want once:\n%s", got, h.errb.String())
			}
			if !tc.want && got != 0 {
				t.Errorf("notice must not be printed:\n%s", h.errb.String())
			}
			if tc.want && !strings.HasPrefix(h.errb.String(), "\n") {
				t.Errorf("notice should follow a blank line: %q", h.errb.String())
			}
			if strings.Contains(h.out.String(), notice) {
				t.Error("notice must go to stderr only")
			}
			if h.apiHits.Load() != 0 {
				t.Error("a fresh cache must not trigger a request")
			}
		})
	}
}

func TestNoticeAfterError(t *testing.T) {
	h := newUpdateHarness(t, "v0.2.0", "NEW")
	writeUpdateCache(t, h.cfgDir, "0.2.0")
	h.e.stderrTTY = true
	if code := h.run("chats", "--user-data", t.TempDir()); code == 0 {
		t.Fatal("expected a failure")
	}
	if n := strings.Count(h.errb.String(), "is available (you have 0.1.1)"); n != 1 {
		t.Errorf("notice printed %d times:\n%s", n, h.errb.String())
	}
}

func TestVersionAndDoctorShowUpdateState(t *testing.T) {
	st := setup(t)
	h := newUpdateHarness(t, "v0.2.0", "NEW")
	h.cfgDir = st.cfgDir

	if code := h.run("version"); code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(h.out.String(), "latest:") {
		t.Errorf("no cache yet, no latest line:\n%s", h.out.String())
	}
	writeUpdateCache(t, h.cfgDir, "0.2.0")
	h.run("version")
	contains(t, h.out.String(), "latest: 0.2.0 (update available)")
	writeUpdateCache(t, h.cfgDir, "0.1.1")
	h.run("version")
	contains(t, h.out.String(), "latest: 0.1.1 (up to date)")

	h.run("--user-data", st.fx.UserData, "doctor")
	contains(t, h.out.String(), "update check", "on (last checked ", ", latest 0.1.1)")

	h.e.version = "dev"
	h.run("--user-data", st.fx.UserData, "doctor")
	contains(t, h.out.String(), "off (dev build)")

	h.e.version = "0.1.1"
	h.e.getenv = func(k string) string {
		if k == "CCSWITCH_NO_UPDATE_CHECK" {
			return "1"
		}
		return ""
	}
	h.run("--user-data", st.fx.UserData, "doctor")
	contains(t, h.out.String(), "off (CCSWITCH_NO_UPDATE_CHECK)")

	h.e.getenv = func(string) string { return "" }
	_ = (&config.Config{UpdateCheck: "off"}).Save(h.cfgDir)
	h.e.app = nil // the loaded app caches the config
	h.run("--user-data", st.fx.UserData, "doctor")
	contains(t, h.out.String(), "off (config)")
}
