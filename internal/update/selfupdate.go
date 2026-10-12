package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// InstallKind describes how the running binary was installed.
type InstallKind int

// Install kinds. Only KindStandalone may be replaced by `ccswitch update`.
const (
	// KindStandalone is a binary from the install script or a manual download.
	KindStandalone InstallKind = iota
	// KindHomebrew is a Homebrew install.
	KindHomebrew
	// KindScoop is a Scoop install.
	KindScoop
	// KindWinget is a winget install.
	KindWinget
	// KindLinuxPackage is a deb, rpm or apk package under /usr/bin.
	KindLinuxPackage
	// KindGoInstall is a `go install` binary.
	KindGoInstall
	// KindUnknown is reserved for installs that cannot be classified.
	KindUnknown
)

// String returns a short human name for the kind.
func (k InstallKind) String() string {
	switch k {
	case KindStandalone:
		return "a standalone download"
	case KindHomebrew:
		return "Homebrew"
	case KindScoop:
		return "Scoop"
	case KindWinget:
		return "winget"
	case KindLinuxPackage:
		return "a system package"
	case KindGoInstall:
		return "go install"
	}
	return "an unknown method"
}

// Install describes the running binary.
type Install struct {
	// Kind is how it was installed.
	Kind InstallKind
	// Path is the resolved executable path.
	Path string
	// Hint is the command to run for package-managed installs; it is empty
	// for KindStandalone and may span several lines.
	Hint string
}

// goInstallHint is the update command for `go install` users.
const goInstallHint = "go install github.com/web-pruthvi-sigmasolve/ccswitch/cmd/ccswitch@latest"

// DetectInstall classifies exe (a resolved executable path) by where it lives.
// goos is the operating system; getenv supplies GOBIN, GOPATH, HOME and
// USERPROFILE.
func DetectInstall(exe string, goos string, getenv func(string) string) Install {
	in := Install{Kind: KindStandalone, Path: exe}
	p := normPath(exe)
	switch {
	case strings.Contains(p, "/cellar/"), strings.Contains(p, "/homebrew/"), strings.Contains(p, "/linuxbrew/"):
		in.Kind, in.Hint = KindHomebrew, "brew upgrade ccswitch"
	case strings.Contains(p, "/scoop/apps/"):
		in.Kind, in.Hint = KindScoop, "scoop update ccswitch"
	case strings.Contains(p, "/winget/packages/"), strings.Contains(p, "/microsoft/winget/"):
		in.Kind, in.Hint = KindWinget, "winget upgrade ccswitch.ccswitch"
	case goos != "windows" && strings.HasPrefix(p, "/usr/bin/"):
		in.Kind = KindLinuxPackage
		in.Hint = "sudo apt upgrade ccswitch   (Debian, Ubuntu)\nsudo dnf upgrade ccswitch   (Fedora, RHEL)"
	case inGoBin(p, getenv):
		in.Kind, in.Hint = KindGoInstall, goInstallHint
	}
	return in
}

// normPath lower-cases p and uses forward slashes so one set of patterns
// serves every OS.
func normPath(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
}

// inGoBin reports whether p is directly inside a Go bin directory.
func inGoBin(p string, getenv func(string) string) bool {
	var dirs []string
	if v := getenv("GOBIN"); v != "" {
		dirs = append(dirs, v)
	}
	if v := getenv("GOPATH"); v != "" {
		for _, d := range filepath.SplitList(v) {
			dirs = append(dirs, filepath.Join(d, "bin"))
		}
	}
	for _, h := range []string{getenv("HOME"), getenv("USERPROFILE")} {
		if h != "" {
			dirs = append(dirs, h+"/go/bin")
		}
	}
	for _, d := range dirs {
		d = strings.TrimRight(normPath(d), "/")
		if d != "" && path.Dir(p) == d {
			return true
		}
	}
	return false
}

// File name suffixes used next to the executable during an update.
const (
	newSuffix = ".ccswitch-new"
	oldSuffix = ".ccswitch-old"
	dlSuffix  = ".ccswitch-dl"
)

// Size limits for downloads.
const (
	maxArchiveBytes  = 150 << 20
	maxBinaryBytes   = 200 << 20
	maxChecksumBytes = 1 << 20
)

// ApplyOptions configure Apply.
type ApplyOptions struct {
	// Exe is the executable to replace.
	Exe string
	// Version is the target version, e.g. "0.2.0" (a leading "v" is accepted).
	Version string
	// Repo is "owner/name"; default DefaultRepo.
	Repo string
	// GOOS and GOARCH pick the archive; default the running system.
	GOOS, GOARCH string
	// BaseURL is a test hook; default "https://github.com".
	BaseURL string
	// Out receives progress lines; default discards them.
	Out io.Writer
	// HTTPClient is used for downloads; its redirect policy is replaced by one
	// that only follows GitHub download hosts. Default: a client with a
	// five-minute timeout.
	HTTPClient *http.Client

	// maxBinary overrides maxBinaryBytes in tests.
	maxBinary int64
}

// allowedRedirectHost reports whether a download may be redirected to host.
// GitHub serves release assets from these hosts.
func allowedRedirectHost(host string) bool {
	switch host {
	case "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com":
		return true
	}
	return false
}

// Apply downloads the release archive for GOOS/GOARCH, verifies its SHA-256
// against checksums.txt, extracts the binary and atomically replaces Exe. It
// returns the installed version. Exe is left untouched on any failure.
func Apply(ctx context.Context, o ApplyOptions) (string, error) {
	ver := trimV(o.Version)
	if _, ok := parseSemver(ver); !ok {
		return "", fmt.Errorf("invalid version %q", o.Version)
	}
	if o.Exe == "" {
		return "", errors.New("no executable path given")
	}
	if o.Repo == "" {
		o.Repo = DefaultRepo
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.BaseURL == "" {
		o.BaseURL = "https://github.com"
	}
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.maxBinary <= 0 {
		o.maxBinary = maxBinaryBytes
	}
	base, err := url.Parse(o.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}
	client := newDownloadClient(o.HTTPClient, base)

	ext, bin := "tar.gz", "ccswitch"
	if o.GOOS == "windows" {
		ext, bin = "zip", "ccswitch.exe"
	}
	archive := fmt.Sprintf("ccswitch_%s_%s_%s.%s", ver, o.GOOS, o.GOARCH, ext)
	root := strings.TrimRight(o.BaseURL, "/") + "/" + o.Repo + "/releases/download/v" + ver + "/"

	// Create the temp files next to the executable first, so a permission
	// problem is reported before anything is downloaded.
	dlPath, newPath := o.Exe+dlSuffix, o.Exe+newSuffix
	_ = os.Remove(dlPath)
	_ = os.Remove(newPath)
	dl, err := os.OpenFile(dlPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("preparing update: %w", err)
	}
	defer func() { _ = dl.Close(); _ = os.Remove(dlPath); _ = os.Remove(newPath) }()

	fmt.Fprintf(o.Out, "Downloading checksums for %s ...\n", ver)
	var sums strings.Builder
	if _, err := download(ctx, client, root+"checksums.txt", &sums, maxChecksumBytes); err != nil {
		return "", err
	}
	want, err := findChecksum(sums.String(), archive)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(o.Out, "Downloading %s ...\n", archive)
	h := sha256.New()
	if _, err := download(ctx, client, root+archive, io.MultiWriter(dl, h), maxArchiveBytes); err != nil {
		return "", err
	}
	if err := dl.Close(); err != nil {
		return "", fmt.Errorf("writing download: %w", err)
	}
	fmt.Fprintln(o.Out, "Verifying SHA-256 checksum ...")
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", archive, want, got)
	}

	fmt.Fprintln(o.Out, "Installing ...")
	if ext == "zip" {
		err = extractZip(dlPath, bin, newPath, o.maxBinary)
	} else {
		err = extractTarGz(dlPath, bin, newPath, o.maxBinary)
	}
	if err != nil {
		return "", err
	}
	if err := os.Chmod(newPath, 0o755); err != nil {
		return "", fmt.Errorf("making the new binary executable: %w", err)
	}
	if err := replaceExe(newPath, o.Exe); err != nil {
		return "", err
	}
	return ver, nil
}

// newDownloadClient copies c (or builds a default client) and restricts
// redirects to GitHub's download hosts over HTTPS, plus the base URL host
// itself (the test hook).
func newDownloadClient(c *http.Client, base *url.URL) *http.Client {
	var cl http.Client
	if c != nil {
		cl = *c
	} else {
		cl.Timeout = 5 * time.Minute
	}
	cl.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		host := strings.ToLower(r.URL.Hostname())
		switch {
		case r.URL.Scheme == "https" && allowedRedirectHost(host):
			return nil
		case r.URL.Host == base.Host && r.URL.Scheme == base.Scheme:
			return nil
		}
		return fmt.Errorf("refusing to follow a redirect to %s", r.URL.Host)
	}
	return &cl
}

// download GETs u into w and returns the byte count. It fails on a non-200
// status or when the body is larger than limit.
func download(ctx context.Context, c *http.Client, u string, w io.Writer, limit int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("building download request: %w", err)
	}
	req.Header.Set("User-Agent", "ccswitch-update")
	resp, err := c.Do(req)
	if err != nil {
		return 0, fmt.Errorf("downloading %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("downloading %s: server answered %s", u, resp.Status)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return n, fmt.Errorf("downloading %s: %w", u, err)
	}
	if n > limit {
		return n, fmt.Errorf("downloading %s: file is larger than %d MB", u, limit>>20)
	}
	return n, nil
}

// findChecksum returns the lower-case hex digest listed for name in a
// checksums.txt body ("<sha256>  <file>" lines).
func findChecksum(body, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s is not listed in checksums.txt", name)
}

// checkEntryName validates an archive entry name. It reports whether the
// entry is the wanted binary; an unsafe path is an error.
func checkEntryName(name, bin string) (bool, error) {
	n := strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(n, "/") {
		return false, fmt.Errorf("archive entry %q has an absolute path", name)
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return false, fmt.Errorf("archive entry %q escapes the folder", name)
		}
	}
	if path.Base(n) == bin && n != bin {
		return false, fmt.Errorf("archive entry %q is not at the archive root", name)
	}
	return name == bin, nil
}

// extractTarGz copies the entry bin from the tar.gz at src to dst.
func extractTarGz(src, bin, dst string, limit int64) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening download: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("reading archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}
		match, err := checkEntryName(hdr.Name, bin)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("archive entry %q is not a regular file", hdr.Name)
		}
		if hdr.Size > limit {
			return fmt.Errorf("archive entry %q is larger than %d MB", hdr.Name, limit>>20)
		}
		if err := writeLimited(dst, tr, limit); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return fmt.Errorf("archive does not contain %s", bin)
	}
	return nil
}

// extractZip copies the entry bin from the zip at src to dst.
func extractZip(src, bin, dst string, limit int64) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("reading archive: %w", err)
	}
	defer zr.Close()
	for _, zf := range zr.File {
		match, err := checkEntryName(zf.Name, bin)
		if err != nil {
			return err
		}
		if !match {
			continue
		}
		if !zf.Mode().IsRegular() {
			return fmt.Errorf("archive entry %q is not a regular file", zf.Name)
		}
		if zf.UncompressedSize64 > uint64(limit) {
			return fmt.Errorf("archive entry %q is larger than %d MB", zf.Name, limit>>20)
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("reading archive: %w", err)
		}
		err = writeLimited(dst, rc, limit)
		_ = rc.Close()
		return err
	}
	return fmt.Errorf("archive does not contain %s", bin)
}

// writeLimited writes r to a new file at dst, failing when r holds more than
// limit bytes.
func writeLimited(dst string, r io.Reader, limit int64) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return fmt.Errorf("creating new binary: %w", err)
	}
	n, err := io.Copy(out, io.LimitReader(r, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}
	if n > limit {
		return fmt.Errorf("extracted binary is larger than %d MB", limit>>20)
	}
	return nil
}

// replaceExe moves newPath over exe. On Windows a running executable cannot
// be overwritten but can be renamed, so it is moved aside first.
func replaceExe(newPath, exe string) error {
	if runtime.GOOS != "windows" {
		if err := os.Rename(newPath, exe); err != nil {
			return fmt.Errorf("replacing %s: %w", exe, err)
		}
		return nil
	}
	old := exe + oldSuffix
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("moving the running program aside: %w", err)
	}
	if err := os.Rename(newPath, exe); err != nil {
		_ = os.Rename(old, exe)
		return fmt.Errorf("replacing %s: %w", exe, err)
	}
	return nil
}

// CleanupOld removes the "<exe>.ccswitch-old" file that a Windows update
// leaves behind. A missing file or any error is ignored.
func CleanupOld(exe string) {
	if exe == "" {
		return
	}
	_ = os.Remove(exe + oldSuffix)
}
