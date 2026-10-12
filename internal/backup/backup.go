// Package backup stores copies of every file ccswitch is about to overwrite
// and can restore them.
package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/web-pruthvi-sigmasolve/ccswitch/internal/fsx"
)

// IDLayout is the time layout of a Set ID.
const IDLayout = "20060102T150405.000Z"

const manifestName = "manifest.json"

// FileRecord describes one backed-up file.
type FileRecord struct {
	// Original is the absolute path the file was copied from.
	Original string `json:"original"`
	// Backup is the copy's path relative to the set directory.
	Backup    string `json:"backup"`
	SessionID string `json:"sessionId"`
	Account   string `json:"account"`
	Org       string `json:"org"`
	Size      int64  `json:"size"`
	Sha256    string `json:"sha256"`
}

// Set is one backup run.
type Set struct {
	ID        string       `json:"id"`
	Dir       string       `json:"-"`
	CreatedAt time.Time    `json:"createdAt"`
	Files     []FileRecord `json:"files"`
	Note      string       `json:"note"`
}

// Begin creates a new backup set directory under root. The manifest is only
// written by Commit.
func Begin(root, note string) (*Set, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create backup root: %w", err)
	}
	for i := 0; i < 50; i++ {
		now := time.Now().UTC()
		id := now.Format(IDLayout)
		dir := filepath.Join(root, id)
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return &Set{ID: id, Dir: dir, CreatedAt: now, Note: note}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create backup set: %w", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	return nil, errors.New("create backup set: could not find a free id")
}

func clean(s string) string {
	if s == "" {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' || r == 0 {
			return '_'
		}
		return r
	}, s)
}

// Add stores data as the backup of originalPath.
func (s *Set) Add(originalPath string, data []byte, account, org, sessionID string) error {
	rel := filepath.Join(clean(account), clean(org), filepath.Base(originalPath))
	for _, f := range s.Files {
		if f.Backup == rel {
			return fmt.Errorf("backup: %s already backed up in this set", rel)
		}
	}
	dst := filepath.Join(s.Dir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	if err := fsx.WriteAtomic(dst, data, 0o600); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	sum := sha256.Sum256(data)
	s.Files = append(s.Files, FileRecord{
		Original: originalPath, Backup: rel, SessionID: sessionID,
		Account: account, Org: org, Size: int64(len(data)),
		Sha256: hex.EncodeToString(sum[:]),
	})
	return nil
}

// Commit writes the manifest atomically.
func (s *Set) Commit() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("backup manifest: %w", err)
	}
	if err := fsx.WriteAtomic(filepath.Join(s.Dir, manifestName), append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("backup manifest: %w", err)
	}
	return nil
}

// Discard removes the set directory if nothing was added; a set holding
// files is left untouched.
func (s *Set) Discard() error {
	if len(s.Files) > 0 {
		return nil
	}
	if err := os.RemoveAll(s.Dir); err != nil {
		return fmt.Errorf("discard backup set: %w", err)
	}
	return nil
}

// List returns all committed sets under root, newest first. A missing root
// yields an empty list; directories without a readable manifest are ignored.
func List(root string) ([]Set, error) {
	ents, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list backups: %w", err)
	}
	var out []Set
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		b, err := os.ReadFile(filepath.Join(dir, manifestName))
		if err != nil {
			continue
		}
		var s Set
		if json.Unmarshal(b, &s) != nil {
			continue
		}
		s.ID, s.Dir = e.Name(), dir
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Latest returns the newest committed set, or an error when there is none.
func Latest(root string) (*Set, error) {
	sets, err := List(root)
	if err != nil {
		return nil, err
	}
	if len(sets) == 0 {
		return nil, errors.New("no backups found")
	}
	return &sets[0], nil
}

// Restore copies every backed-up file over its original. All backups are
// verified against their recorded sha256 before anything is written. With
// dryRun nothing is written. It returns the restored original paths.
func Restore(set *Set, dryRun bool) (restored []string, err error) {
	type item struct {
		rec  FileRecord
		data []byte
	}
	var items []item
	for _, rec := range set.Files {
		data, err := os.ReadFile(filepath.Join(set.Dir, rec.Backup))
		if err != nil {
			return nil, fmt.Errorf("read backup of %s: %w", filepath.Base(rec.Original), err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != rec.Sha256 {
			return nil, fmt.Errorf("backup of %s is corrupted (checksum mismatch); nothing was restored", filepath.Base(rec.Original))
		}
		items = append(items, item{rec, data})
	}
	for _, it := range items {
		if !dryRun {
			mode := fs.FileMode(0o600)
			if st, err := os.Stat(it.rec.Original); err == nil {
				mode = st.Mode().Perm()
			}
			if err := fsx.WriteAtomic(it.rec.Original, it.data, mode); err != nil {
				return restored, fmt.Errorf("restore %s: %w", filepath.Base(it.rec.Original), err)
			}
		}
		restored = append(restored, it.rec.Original)
	}
	return restored, nil
}

// Prune deletes all but the newest keep committed sets and returns the
// removed set directories.
func Prune(root string, keep int) (removed []string, err error) {
	sets, err := List(root)
	if err != nil {
		return nil, err
	}
	if keep < 0 {
		keep = 0
	}
	for i := keep; i < len(sets); i++ {
		if err := os.RemoveAll(sets[i].Dir); err != nil {
			return removed, fmt.Errorf("prune %s: %w", sets[i].ID, err)
		}
		removed = append(removed, sets[i].Dir)
	}
	return removed, nil
}
