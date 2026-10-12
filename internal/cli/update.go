package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/app"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/update"
)

const (
	// releasesURL is shown when an update cannot be done automatically.
	releasesURL = "https://github.com/web-pruthvi-sigmasolve/ccswitch/releases/latest"
	// explicitCheckTimeout bounds the request of `ccswitch update`, which the
	// user asked for and can wait for (the background check uses 3s).
	explicitCheckTimeout = 10 * time.Second
	// noticeWait is how long the end-of-command notice waits for a running
	// background check.
	noticeWait = 200 * time.Millisecond
)

// updateFlags are the flags of `ccswitch update`.
type updateFlags struct {
	check, yes, enableCheck, disableCheck bool
	target                                string
}

func newUpdateCmd(e *env) *cobra.Command {
	var f updateFlags
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Check for a newer ccswitch and install it",
		Long: `Check GitHub for a newer ccswitch release and, for a standalone install,
download it, verify its SHA-256 checksum and replace this program.

If ccswitch was installed with Homebrew, Scoop, winget, a Linux package or
"go install", the command to run is printed instead.

This command and the once-a-day background check are the only things in
ccswitch that use the network. Turn the background check off with
--disable-check, CCSWITCH_NO_UPDATE_CHECK=1 or --no-update-check.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return e.runUpdate(cmd, f) },
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.check, "check", false, "only check for a newer version (ignores the 24 hour interval)")
	fl.BoolVar(&f.yes, "yes", false, "do not ask for confirmation")
	fl.StringVar(&f.target, "version", "", "install this version instead of the latest (e.g. v0.2.0)")
	fl.BoolVar(&f.enableCheck, "enable-check", false, "turn the daily background version check on")
	fl.BoolVar(&f.disableCheck, "disable-check", false, "turn the daily background version check off")
	return cmd
}

// cfgDir returns the ccswitch config folder.
func (e *env) cfgDir() string {
	if e.configDir != "" {
		return e.configDir
	}
	return config.DefaultDir()
}

// executable returns the resolved path of the running program.
func (e *env) executable() (string, error) {
	if e.exePath != nil {
		return e.exePath()
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe, nil
}

// newChecker builds the update checker for this run.
func (e *env) newChecker(timeout time.Duration) *update.Checker {
	return update.New(update.Options{
		ConfigDir: e.cfgDir(),
		Current:   e.version,
		BaseURL:   e.updateAPI,
		Timeout:   timeout,
		Getenv:    e.getenv,
		Now:       e.now,
	})
}

// skipsUpdateCheck reports whether cmd or one of its parents is a command
// that must not trigger or show the background check.
func skipsUpdateCheck(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "update", "completion", "version", "help":
			return true
		}
		if strings.HasPrefix(c.Name(), "__") {
			return true
		}
	}
	return false
}

// startUpdateCheck runs before every command: it removes the leftover of a
// Windows update and, when allowed, starts the background version check.
func (e *env) startUpdateCheck(cmd *cobra.Command) {
	if e.goos == "windows" {
		if exe, err := e.executable(); err == nil {
			update.CleanupOld(exe)
		}
	}
	if e.noUpdateCheck || e.json || e.quiet || !e.stderrTTY || skipsUpdateCheck(cmd) {
		return
	}
	cfg, _ := config.Load(e.cfgDir())
	if update.Disabled(cfg, e.version, e.getenv) {
		return
	}
	e.checker = e.newChecker(update.DefaultTimeout)
	e.checker.Start()
}

// showUpdateNotice prints the one-line notice on stderr when the background
// check found a newer version. It prints at most once per run.
func (e *env) showUpdateNotice() {
	if e.checker == nil || e.noticeDone {
		return
	}
	e.noticeDone = true
	s, ok := e.checker.Result(noticeWait)
	if !ok {
		return
	}
	msg := update.Notice(s)
	if msg == "" {
		return
	}
	term := e.getenv("TERM")
	u := &ui{w: e.errw, color: e.stderrTTY && !e.noColor && e.getenv("NO_COLOR") == "" && term != "dumb"}
	fmt.Fprintf(e.errw, "\n%s\n", u.paint(styleDim, msg))
}

// printCachedLatest adds the "latest:" line to `ccswitch version` when a
// check result is cached. It never touches the network.
func (e *env) printCachedLatest() {
	s, ok := e.newChecker(0).Cached()
	if !ok {
		return
	}
	state := "up to date"
	if s.Available {
		state = "update available"
	}
	fmt.Fprintf(e.out, "latest: %s (%s)\n", s.Latest, state)
}

// updateCheckSummary describes the update check setting for `doctor`.
func (e *env) updateCheckSummary(cfg *config.Config, dir string) string {
	if cfg == nil {
		cfg, _ = config.Load(dir)
	}
	if reason := update.DisabledReason(cfg, e.version, e.getenv); reason != "" {
		return "off (" + reason + ")"
	}
	c := update.New(update.Options{ConfigDir: dir, Current: e.version, Getenv: e.getenv})
	s, ok := c.Cached()
	if !ok || s.CheckedAt.IsZero() {
		return "on (not checked yet)"
	}
	return fmt.Sprintf("on (last checked %s, latest %s)", s.CheckedAt.Local().Format("2006-01-02 15:04"), s.Latest)
}

// updateJSON is the --json output of `ccswitch update --check`.
type updateJSON struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
}

func (e *env) runUpdate(cmd *cobra.Command, f updateFlags) error {
	modes := 0
	for _, b := range []bool{f.check, f.enableCheck, f.disableCheck} {
		if b {
			modes++
		}
	}
	if modes > 1 || (f.check && f.target != "") {
		return &app.HintError{
			Msg:  "Use only one of --check, --enable-check and --disable-check, and not --check with --version.",
			Hint: "Run `ccswitch update --help` for usage.",
		}
	}
	if f.enableCheck || f.disableCheck {
		return e.setUpdateCheck(f.enableCheck)
	}

	u := e.ui()
	arrow := pick(u.ascii, "->", "→")
	var target string
	if f.target != "" {
		v, ok := update.NormalizeVersion(f.target)
		if !ok {
			return &app.HintError{Msg: fmt.Sprintf("%q is not a version number.", f.target), Hint: "Use a form like v0.2.0."}
		}
		target = v
		if v == e.version {
			fmt.Fprintf(e.out, "You are already on version %s.\n", v)
			return nil
		}
	} else {
		if e.version == "dev" || e.version == "" {
			fmt.Fprintln(e.out, "This is a development build, so there is no release to compare it with.")
			return nil
		}
		st, err := e.newChecker(explicitCheckTimeout).Refresh(cmd.Context())
		if err != nil {
			return &app.HintError{
				Msg:  "Could not check for updates.",
				Hint: "Check your internet connection, or look at " + releasesURL + ".",
				Err:  err,
			}
		}
		if f.check && e.json {
			return printJSON(e.out, updateJSON{Current: st.Current, Latest: st.Latest, Available: st.Available, URL: st.URL})
		}
		if !st.Available {
			fmt.Fprintf(e.out, "You are on the latest version (%s).\n", st.Current)
			return nil
		}
		if f.check {
			fmt.Fprintln(e.out, update.Notice(st))
			return nil
		}
		target = st.Latest
	}

	exe, err := e.executable()
	if err != nil {
		return &app.HintError{Msg: "Could not find the running ccswitch program.", Hint: "Download the new version from " + releasesURL + ".", Err: err}
	}
	inst := update.DetectInstall(exe, e.goos, e.getenv)
	if inst.Kind != update.KindStandalone {
		fmt.Fprintf(e.out, "ccswitch %s is available, but this copy was installed with %s. Update it there:\n", target, inst.Kind)
		for _, line := range strings.Split(inst.Hint, "\n") {
			fmt.Fprintf(e.out, "  %s\n", line)
		}
		return nil
	}

	if !f.yes && !e.stdinTTY {
		return &app.HintError{
			Msg:  "Refusing to update without confirmation because input is not a terminal.",
			Hint: "Pass --yes to confirm.",
		}
	}
	ok, err := e.confirm(fmt.Sprintf("Update ccswitch %s %s %s?", e.version, arrow, target), f.yes)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(e.out, "Cancelled. Nothing was changed.")
		return &exitError{code: exitNothing}
	}

	installed, err := update.Apply(cmd.Context(), update.ApplyOptions{
		Exe: exe, Version: target, GOOS: e.goos, GOARCH: runtime.GOARCH, BaseURL: e.updateDL, Out: e.out,
	})
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return &app.HintError{
				Msg:  fmt.Sprintf("Could not replace %s: permission denied.", exe),
				Hint: "Re-run with elevated permissions or reinstall with the install script.",
				Err:  err,
			}
		}
		return &app.HintError{
			Msg:  fmt.Sprintf("The update failed: %v.", err),
			Hint: "The installed version was left unchanged. You can download it from " + releasesURL + ".",
			Err:  err,
		}
	}
	fmt.Fprintf(e.out, "Updated to %s.\n", installed)
	return nil
}

// setUpdateCheck stores the update-check setting and reports the new state.
func (e *env) setUpdateCheck(on bool) error {
	dir := e.cfgDir()
	cfg, err := config.Load(dir)
	if err != nil {
		return &app.HintError{
			Msg:  fmt.Sprintf("Could not read the ccswitch configuration: %v.", err),
			Hint: "Check or remove " + filepath.Join(dir, "config.json") + ".",
			Err:  err,
		}
	}
	cfg.UpdateCheck = config.UpdateCheckOff
	if on {
		cfg.UpdateCheck = config.UpdateCheckAuto
	}
	if err := cfg.Save(dir); err != nil {
		return &app.HintError{Msg: fmt.Sprintf("Could not save the setting: %v.", err), Hint: "Check that " + dir + " is writable.", Err: err}
	}
	if on {
		fmt.Fprintln(e.out, "Update check is now on (at most once a day).")
	} else {
		fmt.Fprintln(e.out, "Update check is now off.")
	}
	return nil
}
