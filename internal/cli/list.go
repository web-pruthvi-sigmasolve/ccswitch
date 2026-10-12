package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ccswitch/ccswitch/internal/identity"
)

type listBucketJSON struct {
	Org   string `json:"org"`
	Path  string `json:"path"`
	Chats int    `json:"chats"`
}

type listAccountJSON struct {
	Index       int              `json:"index"`
	UUID        string           `json:"uuid"`
	Label       string           `json:"label"`
	LabelSource string           `json:"labelSource"`
	Current     bool             `json:"current"`
	Chats       int              `json:"chats"`
	LastActive  string           `json:"lastActive"`
	Projects    []string         `json:"projects"`
	Buckets     []listBucketJSON `json:"buckets"`
}

func newListCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show accounts with labels and a short fingerprint",
		Long:  "Show every account found in the Claude Desktop session store, in the order used by the account numbers elsewhere (`--account 1`).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := e.loadStore()
			if err != nil {
				return err
			}
			u := e.ui()
			if e.json {
				out := make([]listAccountJSON, 0, len(a.Store.Accounts))
				for i, acc := range a.Store.Accounts {
					fp := identity.FingerprintOf(acc)
					l := a.Resolver.Label(acc.UUID)
					name := l.Name
					if name == "" {
						name = "account " + short8(acc.UUID)
					}
					item := listAccountJSON{
						Index: i + 1, UUID: acc.UUID, Label: name, LabelSource: l.Source,
						Current: a.Resolver.IsCurrent(acc.UUID), Chats: fp.Chats,
						Projects: fp.TopProjects, Buckets: []listBucketJSON{},
					}
					if item.Projects == nil {
						item.Projects = []string{}
					}
					if !fp.LastActive.IsZero() {
						item.LastActive = fp.LastActive.UTC().Format(time.RFC3339)
					}
					for _, b := range acc.Buckets {
						item.Buckets = append(item.Buckets, listBucketJSON{Org: b.OrgUUID, Path: b.Path, Chats: len(b.Entries)})
					}
					out = append(out, item)
				}
				return printJSON(e.out, out)
			}
			if len(a.Store.Accounts) == 0 {
				fmt.Fprintln(e.out, "No accounts found in the session store.")
				return nil
			}
			for i, acc := range a.Store.Accounts {
				fp := identity.FingerprintOf(acc)
				fmt.Fprintf(e.out, "%s %s  (%s%s)\n",
					u.paint(styleBold, fmt.Sprintf("[%d]", i+1)), a.Resolver.Display(acc), short8(acc.UUID), u.ellipsis())
				projects := "none"
				if len(fp.TopProjects) > 0 {
					projects = strings.Join(fp.TopProjects, ", ")
				}
				last := "never"
				if !fp.LastActive.IsZero() {
					last = fp.LastActive.Format("2006-01-02 15:04")
				}
				noun := "chats"
				if fp.Chats == 1 {
					noun = "chat"
				}
				fmt.Fprintf(e.out, "   %d %s %s last active %s %s projects: %s\n", fp.Chats, noun, u.sep(), last, u.sep(), projects)
				for _, t := range fp.RecentTitles {
					fmt.Fprintf(e.out, "   %s %s\n", u.paint(styleDim, u.sep()), truncate(t, 70))
				}
			}
			return nil
		},
	}
}

// truncate shortens s to at most n runes, adding "..." when cut.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
}
