package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// flexInt decodes a JSON number (integer or float) into int64 and silently
// ignores any other type, so one odd field never makes an entry unreadable.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	var v float64
	if err := json.Unmarshal(b, &v); err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(v)
	return nil
}

// flexBool decodes a JSON bool and ignores any other type.
type flexBool bool

func (f *flexBool) UnmarshalJSON(b []byte) error {
	var v bool
	if err := json.Unmarshal(b, &v); err != nil {
		*f = false
		return nil
	}
	*f = flexBool(v)
	return nil
}

// sidebarFile holds only the fields ccswitch needs from a sidebar entry.
type sidebarFile struct {
	SessionID      string   `json:"sessionId"`
	CLISessionID   string   `json:"cliSessionId"`
	Title          string   `json:"title"`
	Cwd            string   `json:"cwd"`
	OriginCwd      string   `json:"originCwd"`
	CreatedAt      flexInt  `json:"createdAt"`
	LastActivityAt flexInt  `json:"lastActivityAt"`
	LastFocusedAt  flexInt  `json:"lastFocusedAt"`
	IsArchived     flexBool `json:"isArchived"`
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ReadEntry parses one sidebar file. It tolerates a UTF-8 BOM and CRLF line
// endings, and rejects files whose sessionId is empty or differs from the file
// stem. Raw holds the exact bytes read, ModTime and Mode come from os.Stat.
func ReadEntry(path string) (*Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading sidebar entry: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat sidebar entry: %w", err)
	}
	var sf sidebarFile
	if err := json.Unmarshal(bytes.TrimPrefix(raw, utf8BOM), &sf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
	}
	if sf.SessionID == "" {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), errEmptySessionID)
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	if sf.SessionID != stem {
		return nil, fmt.Errorf("%s: sessionId does not match file name: %w", filepath.Base(path), errSessionIDMismatch)
	}
	return &Entry{
		SessionID:      sf.SessionID,
		CLISessionID:   sf.CLISessionID,
		Title:          sf.Title,
		Cwd:            sf.Cwd,
		OriginCwd:      sf.OriginCwd,
		CreatedAt:      int64(sf.CreatedAt),
		LastActivityAt: int64(sf.LastActivityAt),
		LastFocusedAt:  int64(sf.LastFocusedAt),
		IsArchived:     bool(sf.IsArchived),
		Path:           path,
		Raw:            raw,
		ModTime:        info.ModTime(),
		Mode:           info.Mode(),
	}, nil
}

var (
	errEmptySessionID    = errors.New("sessionId is empty")
	errSessionIDMismatch = errors.New("sessionId mismatch")
)

// ReadBucket populates b.Entries and b.Unreadable from the local_*.json files in
// b.Path using a bounded worker pool. Entries are sorted newest first. When
// transcriptRoot is non-empty each entry's transcript location is resolved.
func ReadBucket(b *Bucket, transcriptRoot string) {
	b.Entries = nil
	b.Unreadable = nil
	files, err := os.ReadDir(b.Path)
	if err != nil {
		return
	}
	var paths []string
	for _, f := range files {
		if isSidebarName(f.Name()) && !f.IsDir() {
			paths = append(paths, filepath.Join(b.Path, f.Name()))
		}
	}
	type result struct {
		e   *Entry
		err error
	}
	results := make([]result, len(paths))
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				e, err := ReadEntry(paths[i])
				if err == nil {
					p, ok := LocateTranscript(transcriptRoot, e)
					e.TranscriptPath, e.TranscriptExists = p, ok
				}
				results[i] = result{e, err}
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			b.Unreadable = append(b.Unreadable, UnreadableEntry{Path: paths[i], Err: r.err})
			continue
		}
		b.Entries = append(b.Entries, r.e)
	}
	sort.SliceStable(b.Entries, func(i, j int) bool {
		ai, aj := b.Entries[i].Activity(), b.Entries[j].Activity()
		if ai != aj {
			return ai > aj
		}
		return b.Entries[i].SessionID < b.Entries[j].SessionID
	})
}
