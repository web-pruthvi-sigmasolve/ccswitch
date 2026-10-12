package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ccswitch/ccswitch/internal/app"
	"github.com/ccswitch/ccswitch/internal/apply"
	"github.com/ccswitch/ccswitch/internal/backup"
	"github.com/ccswitch/ccswitch/internal/config"
	"github.com/ccswitch/ccswitch/internal/plan"
	"github.com/ccswitch/ccswitch/internal/store"
)

type syncFlags struct {
	from, to, org, fromOrg string
	all, skipArchived      bool
	projects, ids          []string
	since, title           string
	mode, onConflict       string
	includeOrphans         bool
	noStrip                bool
	dryRun, yes            bool
}

type opJSON struct {
	SessionID string   `json:"sessionId"`
	Title     string   `json:"title"`
	Project   string   `json:"project"`
	Class     string   `json:"class"`
	Action    string   `json:"action"`
	Reason    string   `json:"reason"`
	DiffKeys  []string `json:"diffKeys"`
	CrossOrg  bool     `json:"crossOrg"`
	AlsoInOrg string   `json:"alsoInOrg,omitempty"`
}

type eventJSON struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

type planJSON struct {
	Mode       string   `json:"mode"`
	OnConflict string   `json:"onConflict"`
	Summary    string   `json:"summary"`
	Adds       int      `json:"adds"`
	Merges     int      `json:"merges"`
	Overwrites int      `json:"overwrites"`
	Skips      int      `json:"skips"`
	Conflicts  int      `json:"conflicts"`
	Orphans    int      `json:"orphans"`
	Ops        []opJSON `json:"ops"`
}

type resultJSON struct {
	Applied   int         `json:"applied"`
	Failed    int         `json:"failed"`
	Skipped   int         `json:"skipped"`
	BackupSet string      `json:"backupSet,omitempty"`
	Events    []eventJSON `json:"events"`
}

type syncJSON struct {
	From   string      `json:"from"`
	To     string      `json:"to"`
	DryRun bool        `json:"dryRun"`
	Plan   planJSON    `json:"plan"`
	Result *resultJSON `json:"result"`
}

func newSyncCmd(e *env) *cobra.Command {
	var fl syncFlags
	cmd := &cobra.Command{
		Use:   "sync --from <account> --to <account> (--all | --project X | --since 7d | --id ID | --title TEXT)",
		Short: "Copy chats from one account to another, safely",
		Long: `Copy the sidebar entries of chosen chats from one account to another.

Chats missing at the destination are added. Chats that exist on both sides are
merged field by field when the source is newer, and kept as they are when the
destination is newer. Every file that would change is backed up first; use
'ccswitch undo' to restore. Archived chats are included; use --skip-archived to leave them out.`,
		Args: cobra.NoArgs,
		Example: `  ccswitch sync --from work --to personal --project ShopifyInforConnector --dry-run
  ccswitch sync --from work --to personal --since 7d --on-conflict prefer-source --yes
  ccswitch sync --from 2 --to 1 --all --mode add-only --yes`,
		RunE: func(cmd *cobra.Command, args []string) error { return runSync(e, &fl) },
	}
	f := cmd.Flags()
	f.StringVar(&fl.from, "from", "", "source account: number, UUID (prefix) or label")
	f.StringVar(&fl.to, "to", "", "destination account: number, UUID (prefix) or label")
	f.StringVar(&fl.org, "org", "", "destination organisation UUID (default: same org as the source, else the destination's main one)")
	f.StringVar(&fl.fromOrg, "from-org", "", "source organisation UUID (default: the source account's most active one)")
	f.BoolVar(&fl.all, "all", false, "select every chat")
	f.StringArrayVar(&fl.projects, "project", nil, "select chats of this project (repeatable); name or end of the folder path")
	f.StringVar(&fl.since, "since", "", "select chats active within this time, e.g. 7d, 2w, 24h")
	f.StringArrayVar(&fl.ids, "id", nil, "select this chat (repeatable); local_<uuid>, <uuid> or a unique prefix")
	f.StringVar(&fl.title, "title", "", "select chats whose title contains this text")
	f.BoolVar(&fl.skipArchived, "skip-archived", false, "leave archived chats out (they are included by default)")
	f.StringVar(&fl.mode, "mode", "merge", "merge | overwrite | add-only")
	f.StringVar(&fl.onConflict, "on-conflict", "keep-dest", "when the destination is newer: keep-dest | prefer-source | skip")
	f.BoolVar(&fl.includeOrphans, "include-orphans", false, "also copy chats whose transcript file is missing")
	f.BoolVar(&fl.noStrip, "no-strip-account-config", false, "keep account-specific settings (connectors) on newly added chats")
	f.BoolVar(&fl.dryRun, "dry-run", false, "show the plan and write nothing")
	f.BoolVar(&fl.yes, "yes", false, "do not ask for confirmation")
	return cmd
}

func runSync(e *env, fl *syncFlags) error {
	if fl.from == "" || fl.to == "" {
		return app.NewHintError("Example: ccswitch sync --from work --to personal --all --dry-run", "Both --from and --to are needed.")
	}
	mode, err := plan.ParseMode(fl.mode)
	if err != nil {
		return &app.HintError{Msg: capitalize(err.Error()) + ".", Hint: "Try --mode merge."}
	}
	onc, err := plan.ParseOnConflict(fl.onConflict)
	if err != nil {
		return &app.HintError{Msg: capitalize(err.Error()) + ".", Hint: "Try --on-conflict keep-dest."}
	}
	filter := app.Filter{All: fl.all, Projects: fl.projects, IDs: fl.ids, TitleMatch: fl.title, SkipArchived: fl.skipArchived}
	if fl.since != "" {
		if filter.Since, err = app.ParseDuration(fl.since); err != nil {
			return err
		}
	}
	a, err := e.loadStore()
	if err != nil {
		return err
	}
	srcAcc, err := a.ResolveAccount(fl.from)
	if err != nil {
		return err
	}
	dstAcc, err := a.ResolveAccount(fl.to)
	if err != nil {
		return err
	}
	srcB, err := a.BucketFor(srcAcc, fl.fromOrg)
	if err != nil {
		return err
	}
	dstB, err := a.DestBucket(dstAcc, srcB, fl.org)
	if err != nil {
		return err
	}
	if srcB.Path == dstB.Path {
		return app.NewHintError("Pick two different accounts, or use --org to choose another organisation.", "The source and destination are the same folder.")
	}
	selected, err := app.Select(srcB, filter, e.now())
	if err != nil {
		return err
	}
	opts := plan.DefaultOptions()
	opts.Mode, opts.OnConflict = mode, onc
	opts.IncludeOrphans = fl.includeOrphans
	opts.StripAccountConfig = !fl.noStrip
	opts.DestAccountBuckets = dstAcc.Buckets
	p, err := plan.Build(srcB, dstB, selected, opts)
	if err != nil {
		return &app.HintError{Msg: "Could not build the plan: " + err.Error() + ".", Hint: "Run `ccswitch doctor` if this looks wrong."}
	}
	fromName, toName := accountName(a, srcAcc), accountName(a, dstAcc)
	backupRoot := config.BackupDir(a.ConfigDir)
	changes := p.Changes()

	if e.json && !fl.dryRun && !fl.yes {
		return &app.HintError{Msg: "JSON output cannot ask for confirmation.", Hint: "Add --yes to apply, or --dry-run to only print the plan."}
	}
	if !e.json {
		printReview(e, a, p, srcAcc, dstAcc, fl)
	}

	if len(changes) == 0 {
		if e.json {
			if err := printJSON(e.out, syncJSON{From: fromName, To: toName, DryRun: fl.dryRun, Plan: toPlanJSON(p)}); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(e.out, "\nNothing to do.")
		}
		return &exitError{code: exitNothing}
	}
	if fl.dryRun {
		if e.json {
			return printJSON(e.out, syncJSON{From: fromName, To: toName, DryRun: true, Plan: toPlanJSON(p)})
		}
		fmt.Fprintf(e.out, "\nDry run: nothing was written. Backups would go to %s\n", backupRoot)
		return nil
	}

	if !e.json {
		ok, err := e.confirm(fmt.Sprintf("\nApply %d change(s) from %s to %s?", len(changes), fromName, toName), fl.yes)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(e.out, "Cancelled. Nothing was written.")
			return &exitError{code: exitNothing}
		}
	}
	release, err := a.Lock()
	if err != nil {
		return err
	}
	defer release()

	u := e.ui()
	if !e.json {
		fmt.Fprintln(e.out)
	}
	res, runErr := apply.Run(p, apply.Options{
		BackupRoot: backupRoot,
		Note:       fmt.Sprintf("sync %s -> %s", fromName, toName),
		Progress: func(ev apply.Event) {
			if e.json || e.quiet || ev.Status == "skipped" {
				return
			}
			printEvent(e, u, ev)
		},
	})
	if res == nil {
		return &app.HintError{Msg: "The run could not start: " + runErr.Error() + ".", Hint: "Nothing was written."}
	}
	if e.json {
		out := syncJSON{From: fromName, To: toName, Plan: toPlanJSON(p), Result: toResultJSON(res)}
		if err := printJSON(e.out, out); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(e.out, "\nDone: %d applied %s %d failed %s %d skipped.\n", res.Applied, u.sep(), res.Failed, u.sep(), res.Skipped)
		if res.BackupSet != nil {
			fmt.Fprintf(e.out, "Backup: %s\n", res.BackupSet.Dir)
			fmt.Fprintln(e.out, "Undo with: ccswitch undo")
		}
		if res.Applied > 0 {
			e.infof("Switch account or restart Claude Desktop to see the chats.")
		}
	}
	if a.Config.BackupRetention > 0 {
		_, _ = backup.Prune(backupRoot, a.Config.BackupRetention)
	}
	if runErr != nil {
		return &exitError{code: exitPartial, err: &app.HintError{Msg: "The run did not finish cleanly: " + runErr.Error() + ".", Hint: "Run `ccswitch undo --list` to check the backups."}}
	}
	if res.Failed > 0 {
		return &exitError{code: exitPartial, err: &app.HintError{
			Msg:  fmt.Sprintf("%d chat(s) could not be written.", res.Failed),
			Hint: "Close Claude Desktop if it holds the files, then run the same command again.",
		}}
	}
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func opGlyph(u *ui, op plan.Op) string {
	switch op.Action {
	case plan.Add:
		return u.glyph(glyphAdd)
	case plan.Merge, plan.Overwrite:
		return u.glyph(glyphMerge)
	}
	switch op.Class {
	case plan.DestNewer, plan.Orphan, plan.Unreadable:
		return u.glyph(glyphWarn)
	}
	return u.glyph(glyphSame)
}

func opTitle(en *store.Entry) string {
	t := truncate(en.Title, 60)
	if t == "" {
		t = "(untitled)"
	}
	return fmt.Sprintf("%s (%s)", t, en.Project())
}

// printReview prints the plan: header, per-chat lines, notes and the summary.
func printReview(e *env, a *app.App, p *plan.Plan, src, dst *store.Account, fl *syncFlags) {
	u := e.ui()
	fmt.Fprintf(e.out, "%s %s -> %s", u.paint(styleBold, "Plan:"),
		accountName(a, src)+"/"+short8(p.SourceBucket.OrgUUID), accountName(a, dst)+"/"+short8(p.DestBucket.OrgUUID))
	if !fileExists(p.DestBucket.Path) {
		fmt.Fprint(e.out, " (new folder)")
	}
	fmt.Fprintf(e.out, "  [mode %s, on conflict %s]\n\n", p.Options.Mode, p.Options.OnConflict)

	hide := e.quiet && (fl.yes || fl.dryRun)
	if !hide {
		for _, op := range p.Ops {
			fmt.Fprintf(e.out, "%s %s  %s\n", opGlyph(u, op), opTitle(op.Source), op.Reason)
			if len(op.DiffKeys) > 0 && (op.Class == plan.DestNewer || op.Action == plan.Merge || op.Action == plan.Overwrite) {
				label := "differs"
				if op.Action == plan.Merge || op.Action == plan.Overwrite {
					label = "will change"
				}
				fmt.Fprintf(e.out, "    %s: %s\n", label, strings.Join(op.DiffKeys, ", "))
			}
		}
		fmt.Fprintln(e.out)
	}
	if p.SourceBucket.OrgUUID != p.DestBucket.OrgUUID {
		fmt.Fprintf(e.out, "%s Source and destination are different organisations; org-scoped connectors may not match.\n", u.glyph(glyphWarn))
	}
	if store.ClaudeDesktopRunning() {
		fmt.Fprintf(e.out, "%s Claude Desktop is running. Changes show up after you switch account or restart it; ccswitch never closes it.\n", u.glyph(glyphWarn))
	}
	fmt.Fprintln(e.out, p.Summary())
	if len(p.Changes()) == 0 && p.Orphans > 0 && !p.Options.IncludeOrphans {
		fmt.Fprintln(e.out, "Use --include-orphans to copy chats whose transcript is missing.")
	}
}

func printEvent(e *env, u *ui, ev apply.Event) {
	var g string
	switch ev.Status {
	case "added":
		g = u.glyph(glyphAdd)
	case "merged", "overwritten":
		g = u.glyph(glyphMerge)
	case "restored":
		g = u.glyph(glyphWarn)
	case "failed":
		g = u.glyph(glyphFail)
	default:
		g = u.glyph(glyphSame)
	}
	line := fmt.Sprintf("%s %-11s %s", g, ev.Status, opTitle(ev.Op.Source))
	if ev.Err != nil {
		line += ": " + ev.Err.Error()
	}
	fmt.Fprintln(e.out, line)
}

func toPlanJSON(p *plan.Plan) planJSON {
	out := planJSON{
		Mode: p.Options.Mode.String(), OnConflict: p.Options.OnConflict.String(), Summary: p.Summary(),
		Adds: p.Adds, Merges: p.Merges, Overwrites: p.Overwrites, Skips: p.Skips, Conflicts: p.Conflicts, Orphans: p.Orphans,
		Ops: []opJSON{},
	}
	for _, op := range p.Ops {
		keys := op.DiffKeys
		if keys == nil {
			keys = []string{}
		}
		out.Ops = append(out.Ops, opJSON{
			SessionID: op.Source.SessionID, Title: op.Source.Title, Project: op.Source.Project(),
			Class: op.Class.String(), Action: op.Action.String(), Reason: op.Reason, DiffKeys: keys, CrossOrg: op.CrossOrg, AlsoInOrg: op.AlsoInOrg,
		})
	}
	return out
}

func toResultJSON(r *apply.Result) *resultJSON {
	out := &resultJSON{Applied: r.Applied, Failed: r.Failed, Skipped: r.Skipped, Events: []eventJSON{}}
	if r.BackupSet != nil {
		out.BackupSet = r.BackupSet.Dir
	}
	for _, ev := range r.Events {
		j := eventJSON{SessionID: ev.Op.Source.SessionID, Status: ev.Status}
		if ev.Err != nil {
			j.Error = ev.Err.Error()
		}
		out.Events = append(out.Events, j)
	}
	return out
}
