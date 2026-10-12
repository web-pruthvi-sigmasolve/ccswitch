package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/app"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/backup"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/fsx"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/merge"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
)

func newUnarchiveCmd(e *env) *cobra.Command {
	var (
		account, org     string
		all, dryRun, yes bool
		ids, projects    []string
	)
	cmd := &cobra.Command{
		Use:   "unarchive --account <account> (--all | --id ID | --project X)",
		Short: "Un-archive chats of one account",
		Long: `Set isArchived to false on archived chats. Only that one value changes: every
other key and the key order stay as they are. Each file is backed up first.`,
		Args: cobra.NoArgs,
		Example: `  ccswitch unarchive --account work --project ShopifyInforConnector
  ccswitch unarchive --account 1 --all --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if account == "" {
				return app.NewHintError("Example: ccswitch unarchive --account 1 --all", "Say which account with --account.")
			}
			a, err := e.loadStore()
			if err != nil {
				return err
			}
			acc, err := a.ResolveAccount(account)
			if err != nil {
				return err
			}
			buckets, err := accountBuckets(a, acc, org)
			if err != nil {
				return err
			}
			f := app.Filter{All: all, Projects: projects, IDs: ids}
			var rows []chatRow
			for _, b := range buckets {
				sel, err := app.Select(b, f, e.now())
				if err != nil {
					if errors.Is(err, app.ErrNoMatch) {
						continue
					}
					return err
				}
				for _, en := range sel {
					if en.IsArchived {
						rows = append(rows, chatRow{b, en})
					}
				}
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].e.Activity() > rows[j].e.Activity() })
			u := e.ui()
			if len(rows) == 0 {
				fmt.Fprintln(e.out, "No archived chats to restore.")
				return &exitError{code: exitNothing}
			}
			fmt.Fprintf(e.out, "Archived chats in %s:\n\n", accountName(a, acc))
			printChatGroups(e, u, groupByProject(rows))
			if dryRun {
				fmt.Fprintf(e.out, "\nDry run: %d chat(s) would be un-archived. Nothing was written.\n", len(rows))
				return nil
			}
			ok, err := e.confirm(fmt.Sprintf("\nUn-archive %d chat(s)?", len(rows)), yes)
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
			return doUnarchive(e, a, u, rows, "unarchive "+accountName(a, acc))
		},
	}
	f := cmd.Flags()
	f.StringVar(&account, "account", "", "account: number, UUID (prefix) or label")
	f.StringVar(&org, "org", "", "only this organisation UUID (default: all of the account's folders)")
	f.BoolVar(&all, "all", false, "select every archived chat")
	f.StringArrayVar(&ids, "id", nil, "select this chat (repeatable); local_<uuid>, <uuid> or a unique prefix")
	f.StringArrayVar(&projects, "project", nil, "select archived chats of this project (repeatable)")
	f.BoolVar(&dryRun, "dry-run", false, "list what would change and write nothing")
	f.BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}

var unarchivePatch = []byte(`{"isArchived":false}`)

func doUnarchive(e *env, a *app.App, u *ui, rows []chatRow, note string) error {
	root := config.BackupDir(a.ConfigDir)
	set, err := backup.Begin(root, note)
	if err != nil {
		return &app.HintError{Msg: "Could not create a backup folder: " + err.Error() + ".", Hint: "Nothing was changed."}
	}
	done, failed := 0, 0
	for _, r := range rows {
		err := unarchiveOne(set, r.b, r.e)
		if err != nil {
			failed++
			fmt.Fprintf(e.out, "%s %s: %v\n", u.glyph(glyphFail), opTitle(r.e), err)
			continue
		}
		done++
		if !e.quiet {
			fmt.Fprintf(e.out, "%s %s\n", u.glyph(glyphMerge), opTitle(r.e)+"  un-archived")
		}
	}
	if len(set.Files) == 0 {
		_ = set.Discard()
	} else if err := set.Commit(); err != nil {
		return &exitError{code: exitPartial, err: &app.HintError{Msg: "Chats were changed but the backup manifest could not be written: " + err.Error() + ".", Hint: "The backup files are in " + set.Dir + "."}}
	}
	fmt.Fprintf(e.out, "\nDone: %d un-archived %s %d failed.\n", done, u.sep(), failed)
	if len(set.Files) > 0 {
		fmt.Fprintf(e.out, "Backup: %s\nUndo with: ccswitch undo\n", set.Dir)
	}
	if done > 0 {
		e.infof("Switch account or restart Claude Desktop to see the changes.")
	}
	if a.Config.BackupRetention > 0 {
		_, _ = backup.Prune(root, a.Config.BackupRetention)
	}
	if failed > 0 {
		return &exitError{code: exitPartial, err: &app.HintError{
			Msg:  fmt.Sprintf("%d chat(s) could not be un-archived.", failed),
			Hint: "Close Claude Desktop if it holds the files, then run the command again.",
		}}
	}
	return nil
}

// unarchiveOne backs up the chat's file, rewrites it with isArchived=false and
// verifies the result, restoring the original on failure.
func unarchiveOne(set *backup.Set, b *store.Bucket, en *store.Entry) error {
	raw, err := os.ReadFile(en.Path)
	if err != nil {
		return fmt.Errorf("cannot read the file: %w", err)
	}
	mode := en.Mode.Perm()
	if st, err := os.Stat(en.Path); err == nil {
		mode = st.Mode().Perm()
	}
	if mode == 0 {
		mode = 0o600
	}
	data, err := merge.Merge(unarchivePatch, raw, merge.DefaultPolicy())
	if err != nil {
		return fmt.Errorf("cannot prepare the new content: %w", err)
	}
	if err := set.Add(en.Path, raw, b.AccountUUID, b.OrgUUID, en.SessionID); err != nil {
		return fmt.Errorf("cannot back up the file: %w", err)
	}
	if err := fsx.WriteAtomic(en.Path, data, mode); err != nil {
		return fmt.Errorf("write failed: %w", err)
	}
	got, verr := store.ReadEntry(en.Path)
	if verr == nil && (got.IsArchived || got.SessionID != en.SessionID) {
		verr = errors.New("the flag is still set")
	}
	if verr != nil {
		if rerr := fsx.WriteAtomic(en.Path, raw, mode); rerr != nil {
			return fmt.Errorf("verification failed (%v) and restoring the original also failed: %w", verr, rerr)
		}
		return fmt.Errorf("verification failed; original restored: %w", verr)
	}
	return nil
}
