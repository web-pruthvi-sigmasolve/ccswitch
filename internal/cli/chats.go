package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ccswitch/ccswitch/internal/app"
	"github.com/ccswitch/ccswitch/internal/store"
)

// chatRow is one chat together with the bucket it lives in.
type chatRow struct {
	b *store.Bucket
	e *store.Entry
}

// chatGroup is the chats of one project folder, newest first.
type chatGroup struct {
	name string
	rows []chatRow
}

// groupByProject groups rows by project folder; groups are ordered by their
// newest chat and rows keep their incoming (newest first) order.
func groupByProject(rows []chatRow) []chatGroup {
	idx := map[string]int{}
	var groups []chatGroup
	for _, r := range rows {
		key := r.e.ProjectDir()
		i, ok := idx[key]
		if !ok {
			i = len(groups)
			idx[key] = i
			groups = append(groups, chatGroup{name: r.e.Project()})
		}
		groups[i].rows = append(groups[i].rows, r)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].rows[0].e.Activity() > groups[j].rows[0].e.Activity()
	})
	return groups
}

// chatMarkers returns " (archived) (no transcript)" style suffixes.
func chatMarkers(en *store.Entry) string {
	var s string
	if en.IsArchived {
		s += " (archived)"
	}
	if !en.TranscriptExists {
		s += " (no transcript)"
	}
	return s
}

// printChatGroups prints grouped chats numbered from 1 and returns the count.
func printChatGroups(e *env, u *ui, groups []chatGroup) int {
	n := 0
	for gi, g := range groups {
		if gi > 0 {
			fmt.Fprintln(e.out)
		}
		noun := "chats"
		if len(g.rows) == 1 {
			noun = "chat"
		}
		fmt.Fprintf(e.out, "%s (%d %s)\n", u.paint(styleBold, g.name), len(g.rows), noun)
		for _, r := range g.rows {
			n++
			title := truncate(r.e.Title, 80)
			if title == "" {
				title = "(untitled)"
			}
			fmt.Fprintf(e.out, "  [%d] %s %s %s %s %s%s\n", n, title, u.sep(), fmtDay(r.e.Activity()), u.sep(), chatID(r.e.SessionID),
				u.paint(styleDim, chatMarkers(r.e)))
		}
	}
	return n
}

type chatJSON struct {
	Org              string `json:"org"`
	SessionID        string `json:"sessionId"`
	CLISessionID     string `json:"cliSessionId"`
	Title            string `json:"title"`
	Project          string `json:"project"`
	Cwd              string `json:"cwd"`
	OriginCwd        string `json:"originCwd"`
	CreatedAt        int64  `json:"createdAt"`
	LastActivityAt   int64  `json:"lastActivityAt"`
	LastFocusedAt    int64  `json:"lastFocusedAt"`
	LastActive       string `json:"lastActive"`
	IsArchived       bool   `json:"isArchived"`
	TranscriptPath   string `json:"transcriptPath"`
	TranscriptExists bool   `json:"transcriptExists"`
	CwdExists        bool   `json:"cwdExists"`
	Path             string `json:"path"`
}

func toChatJSON(r chatRow) chatJSON {
	en := r.e
	c := chatJSON{
		Org: r.b.OrgUUID, SessionID: en.SessionID, CLISessionID: en.CLISessionID, Title: en.Title,
		Project: en.Project(), Cwd: en.Cwd, OriginCwd: en.OriginCwd, CreatedAt: en.CreatedAt,
		LastActivityAt: en.LastActivityAt, LastFocusedAt: en.LastFocusedAt, IsArchived: en.IsArchived,
		TranscriptPath: en.TranscriptPath, TranscriptExists: en.TranscriptExists, CwdExists: en.CwdExists, Path: en.Path,
	}
	if a := en.Activity(); a > 0 {
		c.LastActive = time.UnixMilli(a).UTC().Format(time.RFC3339)
	}
	return c
}

// accountBuckets returns the buckets to read for an account: the one named by
// org, else all of them.
func accountBuckets(a *app.App, acc *store.Account, org string) ([]*store.Bucket, error) {
	if org == "" {
		return acc.Buckets, nil
	}
	b, err := a.BucketFor(acc, org)
	if err != nil {
		return nil, err
	}
	return []*store.Bucket{b}, nil
}

func newChatsCmd(e *env) *cobra.Command {
	var (
		account, org, since string
		projects            []string
		archived            bool
	)
	cmd := &cobra.Command{
		Use:   "chats --account <n|uuid|label>",
		Short: "List the chats of one account, grouped by project",
		Args:  cobra.NoArgs,
		Example: `  ccswitch chats --account work
  ccswitch chats --account 2 --project ShopifyInforConnector --since 7d
  ccswitch chats --account personal --archived --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if account == "" {
				return app.NewHintError("Example: ccswitch chats --account 1", "Say which account to list with --account.")
			}
			a, err := e.loadStore()
			if err != nil {
				return err
			}
			acc, err := a.ResolveAccount(account)
			if err != nil {
				return err
			}
			f := app.Filter{All: true, Projects: projects}
			if since != "" {
				if f.Since, err = app.ParseDuration(since); err != nil {
					return err
				}
			}
			buckets, err := accountBuckets(a, acc, org)
			if err != nil {
				return err
			}
			var rows []chatRow
			for _, b := range buckets {
				sel, err := app.Select(b, f, e.now())
				if err != nil {
					if errorsIsNoMatch(err) {
						continue
					}
					return err
				}
				for _, en := range sel {
					rows = append(rows, chatRow{b, en})
				}
			}
			hidden := 0
			if !archived {
				kept := rows[:0]
				for _, r := range rows {
					if r.e.IsArchived {
						hidden++
						continue
					}
					kept = append(kept, r)
				}
				rows = kept
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].e.Activity() > rows[j].e.Activity() })
			if e.json {
				out := make([]chatJSON, 0, len(rows))
				for _, r := range rows {
					out = append(out, toChatJSON(r))
				}
				return printJSON(e.out, out)
			}
			note := func() {
				if hidden > 0 {
					fmt.Fprintf(e.out, "\n(%d archived chats hidden; add --archived to list them)\n", hidden)
				}
			}
			if len(rows) == 0 {
				fmt.Fprintln(e.out, "No chats match.")
				note()
				return nil
			}
			u := e.ui()
			fmt.Fprintf(e.out, "%s\n\n", u.paint(styleDim, strings.TrimSpace(a.Resolver.Display(acc))))
			printChatGroups(e, u, groupByProject(rows))
			note()
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&account, "account", "", "account to list: number, UUID (prefix) or label")
	f.StringVar(&org, "org", "", "only this organisation UUID (default: all of the account's folders)")
	f.StringArrayVar(&projects, "project", nil, "only this project (repeatable); name or end of the folder path")
	f.StringVar(&since, "since", "", "only chats active within this time, e.g. 7d, 2w, 24h")
	f.BoolVar(&archived, "archived", false, "include archived chats")
	return cmd
}
