package cli

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ccswitch/ccswitch/internal/app"
	"github.com/ccswitch/ccswitch/internal/backup"
	"github.com/ccswitch/ccswitch/internal/config"
)

type backupJSON struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	Note      string `json:"note"`
	Files     int    `json:"files"`
	Dir       string `json:"dir"`
}

func newUndoCmd(e *env) *cobra.Command {
	var (
		list, dryRun, yes bool
		setID             string
	)
	cmd := &cobra.Command{
		Use:   "undo [--list | --set <id>]",
		Short: "Restore the files saved by the last run (or a chosen one)",
		Long: `Restore the files that ccswitch backed up before changing them. By default the
newest backup set is used; --list shows all sets and --set picks one.
Chats that a run newly added are not removed: ccswitch never deletes chats.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := e.load()
			if err != nil {
				return err
			}
			root := config.BackupDir(a.ConfigDir)
			sets, err := backup.List(root)
			if err != nil {
				return &app.HintError{Msg: "Could not read the backups: " + err.Error() + ".", Hint: root}
			}
			if list {
				if e.json {
					out := make([]backupJSON, 0, len(sets))
					for _, s := range sets {
						out = append(out, backupJSON{ID: s.ID, CreatedAt: s.CreatedAt.UTC().Format(time.RFC3339), Note: s.Note, Files: len(s.Files), Dir: s.Dir})
					}
					return printJSON(e.out, out)
				}
				if len(sets) == 0 {
					fmt.Fprintln(e.out, "No backups yet.")
					return nil
				}
				rows := make([][2]string, 0, len(sets))
				for _, s := range sets {
					rows = append(rows, [2]string{s.ID, fmt.Sprintf("%s  %d file(s)  %s", s.CreatedAt.Local().Format("2006-01-02 15:04"), len(s.Files), s.Note)})
				}
				e.ui().table(rows)
				return nil
			}
			if len(sets) == 0 {
				fmt.Fprintln(e.out, "No backups to restore.")
				return &exitError{code: exitNothing}
			}
			set, err := pickSet(sets, setID)
			if err != nil {
				return err
			}
			u := e.ui()
			fmt.Fprintf(e.out, "Backup set %s (%s)", set.ID, set.CreatedAt.Local().Format("2006-01-02 15:04"))
			if set.Note != "" {
				fmt.Fprintf(e.out, " %s %s", u.sep(), set.Note)
			}
			fmt.Fprintf(e.out, "\nIt will restore %d file(s):\n", len(set.Files))
			for _, f := range set.Files {
				fmt.Fprintf(e.out, "  %s  %s/%s/%s\n", u.glyph(glyphMerge), short8(f.Account), short8(f.Org), filepath.Base(f.Original))
			}
			if dryRun {
				if _, err := backup.Restore(set, true); err != nil {
					return &app.HintError{Msg: err.Error() + ".", Hint: "Try an older set with --set."}
				}
				fmt.Fprintln(e.out, "\nDry run: nothing was written.")
				return nil
			}
			ok, err := e.confirm("\nRestore these files? Newer changes in them will be replaced.", yes)
			if err != nil {
				return err
			}
			if !ok {
				fmt.Fprintln(e.out, "Cancelled. Nothing was written.")
				return &exitError{code: exitNothing}
			}
			release, err := a.Lock()
			if err != nil {
				return err
			}
			defer release()
			restored, err := backup.Restore(set, false)
			if err != nil {
				return &exitError{code: exitPartial, err: &app.HintError{
					Msg:  fmt.Sprintf("Restored %d of %d file(s): %s.", len(restored), len(set.Files), err.Error()),
					Hint: "Close Claude Desktop if it holds the files, then run `ccswitch undo --set " + set.ID + "` again.",
				}}
			}
			fmt.Fprintf(e.out, "\nRestored %d file(s).\n", len(restored))
			e.infof("Chats that were newly added by that run stay in place (ccswitch never deletes chats).")
			e.infof("Switch account or restart Claude Desktop to see the changes.")
			return nil
		},
	}
	f := cmd.Flags()
	f.BoolVar(&list, "list", false, "list the backup sets and exit")
	f.StringVar(&setID, "set", "", "restore this backup set (id or unique prefix from --list)")
	f.BoolVar(&dryRun, "dry-run", false, "show what would be restored and write nothing")
	f.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}

func pickSet(sets []backup.Set, id string) (*backup.Set, error) {
	if id == "" {
		return &sets[0], nil
	}
	var found []*backup.Set
	for i := range sets {
		if sets[i].ID == id {
			return &sets[i], nil
		}
		if strings.HasPrefix(sets[i].ID, id) {
			found = append(found, &sets[i])
		}
	}
	switch len(found) {
	case 0:
		return nil, app.NewHintError("Run `ccswitch undo --list` to see the ids.", "There is no backup set %q.", id)
	case 1:
		return found[0], nil
	}
	return nil, app.NewHintError("Use a longer prefix.", "The backup id %q matches %d sets.", id, len(found))
}
