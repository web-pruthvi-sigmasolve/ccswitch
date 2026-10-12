package store

import (
	"os"
	"path/filepath"
	"strings"
)

// EncodeCwd mirrors how Claude Code names a project folder under the
// transcript root: every '/', '\', ':' and space becomes '-'.
// "/var/www/X" -> "-var-www-X"; `C:\Users\me\x` -> "C--Users-me-x".
func EncodeCwd(cwd string) string {
	return strings.NewReplacer("/", "-", `\`, "-", ":", "-", " ", "-").Replace(cwd)
}

// encodeLoose replaces every character other than ASCII letters and digits with
// '-'. Newer Claude Code versions are observed to encode this way ('.' and '_'
// included); it is tried as an extra heuristic before the glob fallback.
func encodeLoose(cwd string) string {
	b := []byte(cwd)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	// Non-ASCII runes become one '-' per byte here; harmless for a heuristic.
	return string(b)
}

// LocateTranscript finds the .jsonl transcript for e under root using only
// stat/readdir (the transcript is never read). It tries the encoded project
// dir, the encoded cwd, a looser encoding and finally every project folder. It
// also sets e.CwdExists. When the transcript is missing, path is the primary
// expected location (empty if e has no cliSessionId or root is empty).
func LocateTranscript(root string, e *Entry) (path string, exists bool) {
	if d := e.ProjectDir(); d != "" {
		fi, err := os.Stat(d)
		e.CwdExists = err == nil && fi.IsDir()
	} else {
		e.CwdExists = false
	}
	id := e.CLISessionID
	if root == "" || id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return "", false
	}
	file := id + ".jsonl"
	var dirs []string
	for _, d := range []string{e.ProjectDir(), e.Cwd} {
		if d != "" {
			dirs = append(dirs, EncodeCwd(d), encodeLoose(d))
		}
	}
	primary := ""
	seen := map[string]bool{}
	for _, d := range dirs {
		p := filepath.Join(root, d, file)
		if primary == "" {
			primary = p
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if isFile(p) {
			return p, true
		}
	}
	if ents, err := os.ReadDir(root); err == nil {
		for _, ent := range ents {
			if !ent.IsDir() {
				continue
			}
			p := filepath.Join(root, ent.Name(), file)
			if isFile(p) {
				return p, true
			}
		}
	}
	return primary, false
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}
