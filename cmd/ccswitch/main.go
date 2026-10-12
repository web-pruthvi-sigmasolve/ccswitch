// Command ccswitch moves Claude Code chats between Claude Desktop accounts.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/app"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/cli"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/tui"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/version"
)

// tuiFlags are the global flags that may accompany interactive mode.
type tuiFlags struct {
	userData, configDir string
	ascii, noColor      bool
}

// parseTUIArgs reports whether args ask for interactive mode: nothing at all,
// or only the global flags --user-data, --config-dir, --ascii and --no-color
// (no positional arguments). Anything else (help, version, subcommands,
// unknown flags) returns false so the normal command line handles it.
func parseTUIArgs(args []string) (tuiFlags, bool) {
	var f tuiFlags
	fs := flag.NewFlagSet("ccswitch", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&f.userData, "user-data", "", "")
	fs.StringVar(&f.configDir, "config-dir", "", "")
	fs.BoolVar(&f.ascii, "ascii", false, "")
	fs.BoolVar(&f.noColor, "no-color", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return tuiFlags{}, false
	}
	return f, true
}

func main() {
	if f, ok := parseTUIArgs(os.Args[1:]); ok && cli.IsTerminal(os.Stdin) && cli.IsTerminal(os.Stdout) {
		os.Exit(runTUI(f))
	}
	// Non-terminal runs (pipes, CI), help, version and subcommands.
	os.Exit(cli.Execute())
}

// runTUI loads the app and runs the interactive interface; it returns the
// process exit code.
func runTUI(f tuiFlags) int {
	a, err := app.Load(app.LoadOptions{UserData: f.userData, ConfigDir: f.configDir})
	if err != nil {
		printErr(err)
		return 1
	}
	if a.StoreErr != nil {
		err := a.RequireStore()
		fmt.Fprintln(os.Stderr, "Folders checked:")
		for _, c := range a.Store.Candidates {
			fmt.Fprintf(os.Stderr, "  %s (%s)\n", c.Path, c.Reason)
		}
		if len(a.Store.Candidates) == 0 {
			fmt.Fprintln(os.Stderr, "  (none)")
		}
		printErr(err)
		return 4
	}
	err = tui.Run(tui.Deps{
		Store: a.Store, Config: a.Config, ConfigDir: a.ConfigDir, Resolver: a.Resolver,
		Version: version.String(), ASCII: f.ascii, NoColor: f.noColor, Lock: a.Lock,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "ccswitch: the interactive screen stopped unexpectedly.")
		return 1
	}
	return 0
}

func printErr(err error) {
	var he *app.HintError
	if errors.As(err, &he) {
		fmt.Fprintf(os.Stderr, "ccswitch: %s\n", he.Msg)
		if he.Hint != "" {
			fmt.Fprintf(os.Stderr, "hint: %s\n", he.Hint)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "ccswitch: %s\n", err)
}
