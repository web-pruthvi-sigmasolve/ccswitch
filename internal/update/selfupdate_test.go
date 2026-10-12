package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectInstall(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	tests := []struct {
		name string
		exe  string
		goos string
		env  map[string]string
		kind InstallKind
		hint string
	}{
		{"standalone linux", "/home/u/.local/bin/ccswitch", "linux", map[string]string{"HOME": "/home/u"}, KindStandalone, ""},
		{"standalone usr local", "/usr/local/bin/ccswitch", "linux", nil, KindStandalone, ""},
		{"standalone windows", `C:\Users\u\AppData\Local\Programs\ccswitch\ccswitch.exe`, "windows", nil, KindStandalone, ""},
		{"homebrew cellar", "/opt/homebrew/Cellar/ccswitch/0.1.1/bin/ccswitch", "darwin", nil, KindHomebrew, "brew upgrade ccswitch"},
		{"homebrew caskroom", "/opt/homebrew/Caskroom/ccswitch/0.1.1/ccswitch", "darwin", nil, KindHomebrew, "brew upgrade ccswitch"},
		{"linuxbrew", "/home/linuxbrew/.linuxbrew/bin/ccswitch", "linux", nil, KindHomebrew, "brew upgrade ccswitch"},
		{"scoop", `C:\Users\u\scoop\apps\ccswitch\current\ccswitch.exe`, "windows", nil, KindScoop, "scoop update ccswitch"},
		{"winget packages", `C:\Users\u\AppData\Local\Microsoft\WinGet\Packages\ccswitch.ccswitch_x\ccswitch.exe`, "windows", nil, KindWinget, "winget upgrade ccswitch.ccswitch"},
		{"winget links", `C:\Users\u\AppData\Local\Microsoft\WinGet\Links\ccswitch.exe`, "windows", nil, KindWinget, "winget upgrade ccswitch.ccswitch"},
		{"linux package", "/usr/bin/ccswitch", "linux", nil, KindLinuxPackage, "sudo apt upgrade ccswitch"},
		{"go install home", "/home/u/go/bin/ccswitch", "linux", map[string]string{"HOME": "/home/u"}, KindGoInstall, goInstallHint},
		{"go install gopath", "/work/go/bin/ccswitch", "linux", map[string]string{"GOPATH": "/work/go"}, KindGoInstall, goInstallHint},
		{"go install gobin", "/opt/gobin/ccswitch", "linux", map[string]string{"GOBIN": "/opt/gobin"}, KindGoInstall, goInstallHint},
		{"go install windows", `C:\Users\u\go\bin\ccswitch.exe`, "windows", map[string]string{"USERPROFILE": `C:\Users\u`}, KindGoInstall, goInstallHint},
		{"go bin subdir is not go install", "/home/u/go/bin/tools/ccswitch", "linux", map[string]string{"HOME": "/home/u"}, KindStandalone, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectInstall(tc.exe, tc.goos, env(tc.env))
			if got.Kind != tc.kind || got.Path != tc.exe {
				t.Errorf("DetectInstall = %+v, want kind %v", got, tc.kind)
			}
			if !strings.HasPrefix(got.Hint, tc.hint) {
				t.Errorf("Hint = %q, want prefix %q", got.Hint, tc.hint)
			}
			if tc.kind == KindStandalone && got.Hint != "" {
				t.Errorf("standalone must have no hint, got %q", got.Hint)
			}
		})
	}
	if lp := DetectInstall("/usr/bin/ccswitch", "linux", env(nil)); !strings.Contains(lp.Hint, "dnf") {
		t.Errorf("linux package hint should name apt and dnf: %q", lp.Hint)
	}
}

type tarEntry struct {
	name string
	body []byte
	typ  byte
	link string
}

func makeTarGz(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o755, Linkname: e.link}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string][]byte, order ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range order {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// releaseServer serves one release: the archive and a checksums.txt.
func releaseServer(t *testing.T, archiveName string, archive []byte, checksum string) *httptest.Server {
	t.Helper()
	base := "/" + DefaultRepo + "/releases/download/v0.2.0/"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case base + "checksums.txt":
			fmt.Fprintf(w, "%s  %s\n%s  other_file.tar.gz\n", checksum, archiveName, strings.Repeat("0", 64))
		case base + archiveName:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeExe(t *testing.T, content string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "ccswitch")
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

func assertNoLeftovers(t *testing.T, exe string) {
	t.Helper()
	ents, err := os.ReadDir(filepath.Dir(exe))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.Name() != filepath.Base(exe) {
			t.Errorf("leftover file %q", e.Name())
		}
	}
}

func TestApplyTarGz(t *testing.T) {
	const name = "ccswitch_0.2.0_linux_amd64.tar.gz"
	archive := makeTarGz(t,
		tarEntry{name: "LICENSE", body: []byte("license")},
		tarEntry{name: "ccswitch", body: []byte("NEW BINARY")},
		tarEntry{name: "README.md", body: []byte("readme")},
	)
	srv := releaseServer(t, name, archive, sha(archive))
	exe := writeExe(t, "OLD BINARY")
	var out bytes.Buffer
	ver, err := Apply(context.Background(), ApplyOptions{
		Exe: exe, Version: "v0.2.0", GOOS: "linux", GOARCH: "amd64", BaseURL: srv.URL, Out: &out,
	})
	if err != nil || ver != "0.2.0" {
		t.Fatalf("Apply = %q, %v", ver, err)
	}
	assertContent(t, exe, "NEW BINARY")
	assertNoLeftovers(t, exe)
	if runtimeUnix() {
		fi, _ := os.Stat(exe)
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("mode = %v", fi.Mode().Perm())
		}
	}
	if !strings.Contains(out.String(), "Verifying") {
		t.Errorf("no progress output: %q", out.String())
	}
}

func TestApplyZip(t *testing.T) {
	const name = "ccswitch_0.2.0_windows_arm64.zip"
	files := map[string][]byte{"README.md": []byte("r"), "ccswitch.exe": []byte("NEW EXE")}
	archive := makeZip(t, files, "README.md", "ccswitch.exe")
	srv := releaseServer(t, name, archive, strings.ToUpper(sha(archive)))
	// The checksum is compared case-insensitively? It must be: uppercase hex is valid.
	exe := writeExe(t, "OLD")
	_, err := Apply(context.Background(), ApplyOptions{
		Exe: exe, Version: "0.2.0", GOOS: "windows", GOARCH: "arm64", BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if runtimeUnix() {
		assertContent(t, exe, "NEW EXE")
	}
}

func TestApplyFailuresLeaveExeUntouched(t *testing.T) {
	const name = "ccswitch_0.2.0_linux_amd64.tar.gz"
	good := makeTarGz(t, tarEntry{name: "ccswitch", body: []byte("NEW")})
	tests := []struct {
		name     string
		archive  []byte
		checksum string
		maxBin   int64
		want     string
	}{
		{"bad checksum", good, strings.Repeat("a", 64), 0, "checksum mismatch"},
		{"missing entry", makeTarGz(t, tarEntry{name: "README.md", body: []byte("x")}), "", 0, "does not contain ccswitch"},
		{"nested entry", makeTarGz(t, tarEntry{name: "dir/ccswitch", body: []byte("x")}), "", 0, "not at the archive root"},
		{"dot dot entry", makeTarGz(t, tarEntry{name: "../ccswitch", body: []byte("x")}), "", 0, "escapes"},
		{"absolute entry", makeTarGz(t, tarEntry{name: "/etc/passwd", body: []byte("x")}), "", 0, "absolute"},
		{"symlink entry", makeTarGz(t, tarEntry{name: "ccswitch", typ: tar.TypeSymlink, link: "/bin/sh"}), "", 0, "not a regular file"},
		{"oversized entry", makeTarGz(t, tarEntry{name: "ccswitch", body: bytes.Repeat([]byte("x"), 100)}), "", 50, "larger than"},
		{"not gzip", []byte("this is not an archive"), "", 0, "reading archive"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sum := tc.checksum
			if sum == "" {
				sum = sha(tc.archive)
			}
			srv := releaseServer(t, name, tc.archive, sum)
			exe := writeExe(t, "OLD BINARY")
			_, err := Apply(context.Background(), ApplyOptions{
				Exe: exe, Version: "0.2.0", GOOS: "linux", GOARCH: "amd64", BaseURL: srv.URL, maxBinary: tc.maxBin,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			assertContent(t, exe, "OLD BINARY")
			assertNoLeftovers(t, exe)
		})
	}
}

func TestApplyZipFailures(t *testing.T) {
	const name = "ccswitch_0.2.0_windows_amd64.zip"
	tests := []struct {
		name  string
		files map[string][]byte
		order []string
		max   int64
		want  string
	}{
		{"missing entry", map[string][]byte{"README.md": []byte("x")}, []string{"README.md"}, 0, "does not contain ccswitch.exe"},
		{"nested entry", map[string][]byte{"bin/ccswitch.exe": []byte("x")}, []string{"bin/ccswitch.exe"}, 0, "not at the archive root"},
		{"oversized entry", map[string][]byte{"ccswitch.exe": bytes.Repeat([]byte("x"), 100)}, []string{"ccswitch.exe"}, 50, "larger than"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			archive := makeZip(t, tc.files, tc.order...)
			srv := releaseServer(t, name, archive, sha(archive))
			exe := writeExe(t, "OLD BINARY")
			_, err := Apply(context.Background(), ApplyOptions{
				Exe: exe, Version: "0.2.0", GOOS: "windows", GOARCH: "amd64", BaseURL: srv.URL, maxBinary: tc.max,
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
			assertContent(t, exe, "OLD BINARY")
			assertNoLeftovers(t, exe)
		})
	}
}

func TestApplyDownloadErrors(t *testing.T) {
	exe := writeExe(t, "OLD BINARY")
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := Apply(context.Background(), ApplyOptions{Exe: exe, Version: "0.2.0", GOOS: "linux", GOARCH: "amd64", BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected an error for a 404")
	}
	assertContent(t, exe, "OLD BINARY")
	assertNoLeftovers(t, exe)

	// Archive not listed in checksums.txt.
	archive := makeTarGz(t, tarEntry{name: "ccswitch", body: []byte("x")})
	srv2 := releaseServer(t, "ccswitch_0.2.0_linux_amd64.tar.gz", archive, sha(archive))
	_, err = Apply(context.Background(), ApplyOptions{Exe: exe, Version: "0.2.0", GOOS: "linux", GOARCH: "arm64", BaseURL: srv2.URL})
	if err == nil {
		t.Fatal("expected an error for an unlisted archive")
	}

	if _, err := Apply(context.Background(), ApplyOptions{Exe: exe, Version: "banana"}); err == nil {
		t.Error("expected an error for a bad version")
	}
}

func TestApplyPermissionDenied(t *testing.T) {
	if !runtimeUnix() || os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "ccswitch")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	_, err := Apply(context.Background(), ApplyOptions{Exe: exe, Version: "0.2.0", GOOS: "linux", GOARCH: "amd64", BaseURL: "http://127.0.0.1:1"})
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("err = %v, want a permission error", err)
	}
}

func TestRedirectPolicy(t *testing.T) {
	const name = "ccswitch_0.2.0_linux_amd64.tar.gz"
	// A server that redirects every download to a foreign host.
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the foreign host must not be contacted")
	}))
	defer evil.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	exe := writeExe(t, "OLD BINARY")
	_, err := Apply(context.Background(), ApplyOptions{Exe: exe, Version: "0.2.0", GOOS: "linux", GOARCH: "amd64", BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("err = %v, want a redirect refusal", err)
	}
	assertContent(t, exe, "OLD BINARY")
	_ = name

	for host, want := range map[string]bool{
		"github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true,
		"evil.example": false, "github.com.evil.example": false,
	} {
		if allowedRedirectHost(host) != want {
			t.Errorf("allowedRedirectHost(%q) = %v", host, !want)
		}
	}
}

func TestFindChecksum(t *testing.T) {
	body := "AAA  one.tar.gz\r\nbbb *two.zip\n\nccc three\n"
	if got, err := findChecksum(body, "one.tar.gz"); err != nil || got != "aaa" {
		t.Errorf("one = %q, %v", got, err)
	}
	if got, err := findChecksum(body, "two.zip"); err != nil || got != "bbb" {
		t.Errorf("two = %q, %v", got, err)
	}
	if _, err := findChecksum(body, "missing"); err == nil {
		t.Error("expected an error")
	}
}

func TestCleanupOld(t *testing.T) {
	exe := writeExe(t, "x")
	old := exe + oldSuffix
	if err := os.WriteFile(old, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	CleanupOld(exe)
	if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("old file still there: %v", err)
	}
	CleanupOld(exe) // no file: no panic
	CleanupOld("")
	assertContent(t, exe, "x")
}

func runtimeUnix() bool { return filepath.Separator == '/' }
