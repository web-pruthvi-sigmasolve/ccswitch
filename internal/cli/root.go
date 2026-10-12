// Package cli implements the ccswitch command line (cobra commands, output
// helpers and exit codes).
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ccswitch/ccswitch/internal/app"
	"github.com/ccswitch/ccswitch/internal/store"
	"github.com/ccswitch/ccswitch/internal/version"
)

// Exit codes (PLAN.md section 3.8).
const (
	exitOK           = 0
	exitUser         = 1
	exitNothing      = 2
	exitPartial      = 3
	exitStoreMissing = 4
)

// exitError makes a command finish with a specific exit code. When Err is nil
// nothing more is printed (the command already explained itself).
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit status %d", e.code)
}

func (e *exitError) Unwrap() error { return e.err }

// env is the per-run state shared by all commands: streams, terminal facts,
// global flags and the lazily loaded app. It replaces package-level globals.
type env struct {
	in        io.Reader
	inr       *bufio.Reader
	out, errw io.Writer

	stdinTTY, stdoutTTY bool
	getenv              func(string) string
	goos                string
	now                 func() time.Time

	userData, configDir         string
	json, noColor, ascii, quiet bool

	app *app.App
}

// newEnv builds the environment for a run from the given streams.
func newEnv(in io.Reader, out, errw io.Writer) *env {
	e := &env{in: in, out: out, errw: errw, getenv: os.Getenv, goos: runtime.GOOS, now: time.Now}
	if f, ok := in.(*os.File); ok {
		e.stdinTTY = IsTerminal(f)
	}
	if f, ok := out.(*os.File); ok {
		e.stdoutTTY = IsTerminal(f)
	}
	return e
}

// ui returns the output helper for stdout.
func (e *env) ui() *ui {
	term := e.getenv("TERM")
	color := e.stdoutTTY && !e.noColor && e.getenv("NO_COLOR") == "" && term != "dumb"
	ascii := e.ascii || !e.stdoutTTY || term == "dumb" ||
		(e.goos == "windows" && e.getenv("WT_SESSION") == "" && e.getenv("TERM_PROGRAM") == "")
	if e.app != nil && e.app.Config != nil && e.app.Config.ASCII {
		ascii = true
	}
	return &ui{w: e.out, color: color, ascii: ascii}
}

// load returns the app, loading it on first use.
func (e *env) load() (*app.App, error) {
	if e.app != nil {
		return e.app, nil
	}
	a, err := app.Load(app.LoadOptions{UserData: e.userData, ConfigDir: e.configDir})
	if err != nil {
		return nil, err
	}
	e.app = a
	return a, nil
}

// loadStore is load plus a check that the session store exists. When it does
// not, the candidates that were examined are listed on stderr.
func (e *env) loadStore() (*app.App, error) {
	a, err := e.load()
	if err != nil {
		return nil, err
	}
	if err := a.RequireStore(); err != nil {
		fmt.Fprintln(e.errw, "Folders checked:")
		for _, c := range a.Store.Candidates {
			fmt.Fprintf(e.errw, "  %s (%s)\n", c.Path, c.Reason)
		}
		if len(a.Store.Candidates) == 0 {
			fmt.Fprintln(e.errw, "  (none)")
		}
		return nil, err
	}
	return a, nil
}

// infof prints a line unless --quiet is set.
func (e *env) infof(format string, args ...any) {
	if !e.quiet {
		fmt.Fprintf(e.out, format+"\n", args...)
	}
}

// Execute runs ccswitch with the process arguments and streams and returns the
// exit code.
func Execute() int {
	return Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// Run runs ccswitch with explicit arguments and streams and returns the exit
// code. Terminal behaviour is derived from the streams when they are files.
func Run(args []string, in io.Reader, out, errw io.Writer) int {
	return newEnv(in, out, errw).run(args)
}

// PrintHelp writes the root help text to w followed by note, and returns 0.
// The no-argument terminal entry point uses it until the TUI is wired in.
func PrintHelp(w io.Writer, note string) int {
	e := newEnv(os.Stdin, w, w)
	root := newRoot(e)
	root.SetOut(w)
	_ = root.Help()
	if note != "" {
		fmt.Fprintf(w, "\n%s\n", note)
	}
	return exitOK
}

func (e *env) run(args []string) int {
	root := newRoot(e)
	root.SetArgs(args)
	root.SetIn(e.in)
	root.SetOut(e.out)
	root.SetErr(e.errw)
	err := root.Execute()
	if err == nil {
		return exitOK
	}
	return e.report(err)
}

// report prints err for the user and returns the exit code.
func (e *env) report(err error) int {
	var ee *exitError
	if errors.As(err, &ee) && ee.err == nil {
		return ee.code
	}
	var he *app.HintError
	msg := err.Error()
	hint := ""
	if errors.As(err, &he) {
		msg, hint = he.Msg, he.Hint
	}
	fmt.Fprintf(e.errw, "ccswitch: %s\n", msg)
	if hint != "" {
		fmt.Fprintf(e.errw, "hint: %s\n", hint)
	}
	return exitCodeFor(err)
}

func exitCodeFor(err error) int {
	var ee *exitError
	switch {
	case errors.As(err, &ee):
		return ee.code
	case errors.Is(err, store.ErrStoreNotFound):
		return exitStoreMissing
	case errors.Is(err, app.ErrNoMatch):
		return exitNothing
	}
	return exitUser
}

// newRoot builds the command tree for one run.
func newRoot(e *env) *cobra.Command {
	root := &cobra.Command{
		Use:   "ccswitch",
		Short: "Move Claude Code chats between Claude Desktop accounts safely",
		Long: `ccswitch makes Claude Code chats visible in another signed-in Claude Desktop
account. It never overwrites blindly, backs up every file before writing, and
can undo the last change. No network, no telemetry.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.String(),
	}
	root.SetVersionTemplate("{{.Version}}\n")
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		msg := err.Error()
		if msg != "" {
			msg = strings.ToUpper(msg[:1]) + msg[1:]
		}
		return &app.HintError{Msg: strings.TrimSuffix(msg, ".") + ".", Hint: "Run `" + c.CommandPath() + " --help` for usage."}
	})
	pf := root.PersistentFlags()
	pf.StringVar(&e.userData, "user-data", "", "Claude Desktop user-data folder (default: auto-detect, or $CLAUDE_USER_DATA)")
	pf.StringVar(&e.configDir, "config-dir", "", "ccswitch config folder (labels, backups, lock)")
	pf.BoolVar(&e.json, "json", false, "machine-readable JSON output (list, chats, sync, undo --list)")
	pf.BoolVar(&e.noColor, "no-color", false, "disable colour (NO_COLOR is also honoured)")
	pf.BoolVar(&e.ascii, "ascii", false, "use ASCII symbols only")
	pf.BoolVar(&e.quiet, "quiet", false, "print less: no notes or progress lines")

	root.AddCommand(
		newListCmd(e),
		newChatsCmd(e),
		newSyncCmd(e),
		newUnarchiveCmd(e),
		newLabelCmd(e),
		newUndoCmd(e),
		newDoctorCmd(e),
		newVersionCmd(e),
	)
	return root
}

func newVersionCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the ccswitch version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(e.out, version.String())
			return nil
		},
	}
}

// accountName is a short plain name for an account: its label (or cached
// profile name), else "account <uuid8>". It never includes an email.
func accountName(a *app.App, acc *store.Account) string {
	if n := a.Resolver.Label(acc.UUID).Name; n != "" {
		return n
	}
	return "account " + short8(acc.UUID)
}

func short8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// chatID is the 8-character id shown in lists.
func chatID(sessionID string) string {
	return short8(strings.TrimPrefix(sessionID, "local_"))
}

func fmtDay(ms int64) string {
	if ms <= 0 {
		return "unknown"
	}
	return time.UnixMilli(ms).Format("2006-01-02")
}

func fmtMinute(ms int64) string {
	if ms <= 0 {
		return "never"
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04")
}

// bucketLabel describes a bucket as "<account name>/<org8>".
func bucketLabel(a *app.App, b *store.Bucket) string {
	name := short8(b.AccountUUID)
	if acc := a.Store.Account(b.AccountUUID); acc != nil {
		name = accountName(a, acc)
	}
	return name + "/" + short8(b.OrgUUID)
}

func errorsIsNoMatch(err error) bool { return errors.Is(err, app.ErrNoMatch) }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
