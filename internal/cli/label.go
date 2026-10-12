package cli

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/ccswitch/ccswitch/internal/app"
)

const maxLabelLen = 64

func newLabelCmd(e *env) *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   `label [<account> "<name>"]`,
		Short: "Give an account a readable name (or show the names)",
		Long: `Name an account so lists and prompts show it instead of a UUID.

  ccswitch label                   show the current names
  ccswitch label 1 "work"          name account 1
  ccswitch label --clear 1         remove the name of account 1`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := e.loadStore()
			if err != nil {
				return err
			}
			switch {
			case clear:
				if len(args) != 1 {
					return app.NewHintError(`Example: ccswitch label --clear 1`, "Say which account to clear.")
				}
				acc, err := a.ResolveAccount(args[0])
				if err != nil {
					return err
				}
				a.Config.SetLabel(acc.UUID, "")
				if err := a.Config.Save(a.ConfigDir); err != nil {
					return saveErr(err)
				}
				fmt.Fprintf(e.out, "Removed the name of account %s.\n", short8(acc.UUID))
				return nil
			case len(args) == 0:
				return showLabels(e, a)
			case len(args) == 1:
				return app.NewHintError(`Example: ccswitch label 1 "work"  (or --clear to remove a name)`, "Give a name for the account.")
			}
			acc, err := a.ResolveAccount(args[0])
			if err != nil {
				return err
			}
			name := strings.TrimSpace(args[1])
			if name == "" {
				return app.NewHintError("Use --clear to remove a name.", "The name is empty.")
			}
			if len([]rune(name)) > maxLabelLen {
				return app.NewHintError("Use a shorter name.", "The name is longer than %d characters.", maxLabelLen)
			}
			for _, r := range name {
				if unicode.IsControl(r) {
					return app.NewHintError("Use plain text.", "The name contains control characters.")
				}
			}
			a.Config.SetLabel(acc.UUID, name)
			if err := a.Config.Save(a.ConfigDir); err != nil {
				return saveErr(err)
			}
			fmt.Fprintf(e.out, "Account %s is now called %q.\n", short8(acc.UUID), name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "remove the name of the given account")
	return cmd
}

func saveErr(err error) error {
	return &app.HintError{Msg: "Could not save the configuration: " + err.Error() + ".", Hint: "Check that the ccswitch config folder is writable."}
}

func showLabels(e *env, a *app.App) error {
	n := 0
	for i, acc := range a.Store.Accounts {
		name := a.Config.Labels[acc.UUID]
		if name == "" {
			continue
		}
		n++
		fmt.Fprintf(e.out, "[%d] %s  (%s%s)\n", i+1, name, short8(acc.UUID), e.ui().ellipsis())
	}
	if n == 0 {
		fmt.Fprintln(e.out, `No names set yet. Example: ccswitch label 1 "work"`)
	}
	return nil
}
