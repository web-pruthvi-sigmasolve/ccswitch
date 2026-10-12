package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/app"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/backup"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/config"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/store"
	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/version"
)

func newDoctorCmd(e *env) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Print paths, candidates, versions and permissions (for bug reports)",
		Long: `Print what ccswitch found on this machine: the folders it checked, the chosen
store, counts, the Claude Desktop version and whether the store is writable.
It never prints chat contents, emails or tokens. It works even without a store.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			u := e.ui()
			var problems []string
			a, err := e.load()
			if err != nil {
				problems = append(problems, err.Error())
				// Fall back to discovery alone so the report is still useful.
				st, _ := store.Discover(store.Options{UserDataOverride: e.userData})
				dir := e.configDir
				if dir == "" {
					dir = config.DefaultDir()
				}
				a = &app.App{Store: st, ConfigDir: dir}
				if st != nil && len(st.Accounts) == 0 {
					a.StoreErr = store.ErrStoreNotFound
				}
			}
			st := a.Store
			if st == nil {
				st = &store.Store{}
			}

			rows := [][2]string{
				{"version", version.String()},
				{"os/arch", runtime.GOOS + "/" + runtime.GOARCH},
				{"go", runtime.Version()},
				{"update check", e.updateCheckSummary(a.Config, a.ConfigDir)},
			}
			chosen := st.UserData
			if a.StoreErr != nil {
				chosen = "(none found)"
			}
			rows = append(rows, [2]string{"user-data folder", chosen})
			fmt.Fprintln(e.out, u.paint(styleBold, "ccswitch doctor"))
			u.table(rows)

			fmt.Fprintf(e.out, "\n%s\n", u.paint(styleBold, "Candidates examined"))
			printCandidates(e.out, st.Candidates)

			fmt.Fprintln(e.out)
			backupDir := config.BackupDir(a.ConfigDir)
			sets, _ := backup.List(backupDir)
			rows = [][2]string{
				{"transcript root", fmt.Sprintf("%s (%s)", st.TranscriptRoot, existsWord(st.TranscriptRoot))},
				{"config folder", fmt.Sprintf("%s (%s)", a.ConfigDir, existsWord(a.ConfigDir))},
				{"backup folder", fmt.Sprintf("%s (%d set(s))", backupDir, len(sets))},
			}
			buckets, entries, unreadable := 0, 0, 0
			var badNames []string
			for _, acc := range st.Accounts {
				for _, b := range acc.Buckets {
					buckets++
					entries += len(b.Entries)
					unreadable += len(b.Unreadable)
					for _, un := range b.Unreadable {
						if len(badNames) < 5 {
							badNames = append(badNames, filepath.Base(un.Path))
						}
					}
				}
			}
			rows = append(rows,
				[2]string{"accounts", strconv.Itoa(len(st.Accounts))},
				[2]string{"buckets", strconv.Itoa(buckets)},
				[2]string{"chats", strconv.Itoa(entries)},
				[2]string{"unreadable entries", strconv.Itoa(unreadable)},
				[2]string{"Claude Desktop running", yesNo(store.ClaudeDesktopRunning())},
				[2]string{"Claude Desktop version", appVersion(st)},
				[2]string{"store writable", writableCheck(st)},
			)
			u.table(rows)
			for _, n := range badNames {
				fmt.Fprintf(e.out, "  unreadable: %s\n", n)
			}
			if a.StoreErr != nil {
				fmt.Fprintf(e.out, "\n%s No session store was found. Set CLAUDE_USER_DATA or pass --user-data <dir> to point at it.\n", u.glyph(glyphWarn))
			}
			for _, p := range problems {
				fmt.Fprintf(e.out, "\n%s %s\n", u.glyph(glyphWarn), p)
			}
			return nil
		},
	}
}

// missingFolderReason is the store.Candidate.Reason of a location that is not
// on disk.
const missingFolderReason = "folder does not exist"

// printCandidates lists every candidate that exists (or was chosen) and
// collapses the ones whose folder does not exist into a single line.
func printCandidates(w io.Writer, cands []store.Candidate) {
	if len(cands) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	missing := 0
	var kinds []string
	for _, c := range cands {
		if c.Reason == missingFolderReason && !c.Chosen {
			missing++
			if k := candidateKind(c.Path); k != "" && !slices.Contains(kinds, k) {
				kinds = append(kinds, k)
			}
			continue
		}
		mark := " "
		if c.Chosen {
			mark = "*"
		}
		fmt.Fprintf(w, "  %s %s\n      %s\n", mark, c.Path, c.Reason)
	}
	if missing > 0 {
		what := ""
		if len(kinds) > 0 {
			what = " (" + strings.Join(kinds, ", ") + ", …)"
		}
		fmt.Fprintf(w, "  %d other locations checked%s do not exist\n", missing, what)
	}
}

// candidateKind names the packaging flavour of a candidate path, or "" when it
// is a plain location.
func candidateKind(p string) string {
	s := strings.ToLower(filepath.ToSlash(p))
	switch {
	case strings.Contains(s, "/snap/"):
		return "snap"
	case strings.Contains(s, "/.var/app/"), strings.Contains(s, "flatpak"):
		return "flatpak"
	}
	return ""
}

func existsWord(p string) string {
	if p != "" && fileExists(p) {
		return "exists"
	}
	return "missing"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// appVersion reads toolSurfaceSnapshot.appVersion from the newest entry. Only
// that one field is decoded.
func appVersion(st *store.Store) string {
	var newest *store.Entry
	for _, acc := range st.Accounts {
		for _, b := range acc.Buckets {
			for _, en := range b.Entries {
				if newest == nil || en.Activity() > newest.Activity() {
					newest = en
				}
			}
		}
	}
	if newest == nil {
		return "unknown (no chats)"
	}
	var doc struct {
		Snapshot struct {
			AppVersion string `json:"appVersion"`
		} `json:"toolSurfaceSnapshot"`
	}
	raw := bytes.TrimPrefix(newest.Raw, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Snapshot.AppVersion == "" {
		return "unknown"
	}
	return doc.Snapshot.AppVersion
}

// writableCheck creates and removes a temp file in the first bucket.
func writableCheck(st *store.Store) string {
	for _, acc := range st.Accounts {
		for _, b := range acc.Buckets {
			f, err := os.CreateTemp(b.Path, ".ccswitch-doctor-*")
			if err != nil {
				return "no (" + err.Error() + ")"
			}
			name := f.Name()
			_ = f.Close()
			if err := os.Remove(name); err != nil {
				return "partly (could not remove the test file: " + err.Error() + ")"
			}
			return "yes"
		}
	}
	return "unknown (no buckets)"
}
