// Package apply executes a plan.Plan: it backs up every destination file it
// is about to change, writes atomically, verifies the result and restores
// the backup when verification fails.
package apply

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ccswitch/ccswitch/internal/backup"
	"github.com/ccswitch/ccswitch/internal/fsx"
	"github.com/ccswitch/ccswitch/internal/merge"
	"github.com/ccswitch/ccswitch/internal/plan"
)

// Options control Run.
type Options struct {
	// DryRun computes everything but writes nothing.
	DryRun bool
	// BackupRoot is where backup sets are created.
	BackupRoot string
	// Note is stored in the backup manifest.
	Note string
	// Progress, when set, is called for every event as it happens.
	Progress func(ev Event)

	// verify replaces the post-write verification (tests only).
	verify func(path, wantSessionID string) error
}

// Event reports the outcome for one op.
type Event struct {
	Op plan.Op
	// Status is one of "added", "merged", "overwritten", "skipped",
	// "failed" or "restored". Dry runs report the status that would result.
	Status string
	Err    error
}

// Result summarises a run.
type Result struct {
	Applied, Failed, Skipped int
	// BackupSet is nil in dry runs and when nothing was overwritten.
	BackupSet *backup.Set
	Events    []Event
}

// Run executes the plan. One failing op never aborts the others; failures are
// reported in the result. The returned error is only for run-level problems
// such as being unable to write the backup manifest.
func Run(p *plan.Plan, opts Options) (*Result, error) {
	if p == nil {
		return nil, errors.New("apply: nil plan")
	}
	r := &Result{}
	emit := func(op plan.Op, status string, err error) {
		ev := Event{Op: op, Status: status, Err: err}
		r.Events = append(r.Events, ev)
		if opts.Progress != nil {
			opts.Progress(ev)
		}
	}
	var set *backup.Set
	for _, op := range p.Ops {
		if op.Action == plan.Skip {
			r.Skipped++
			emit(op, "skipped", nil)
			continue
		}
		status, restored, err := runOp(p, op, &opts, &set)
		if restored {
			emit(op, "restored", nil)
		}
		if err != nil {
			r.Failed++
			emit(op, "failed", err)
			continue
		}
		r.Applied++
		emit(op, status, nil)
	}
	if set != nil {
		if len(set.Files) == 0 {
			if err := set.Discard(); err != nil {
				return r, err
			}
		} else {
			r.BackupSet = set
			if err := set.Commit(); err != nil {
				return r, fmt.Errorf("apply: %w", err)
			}
		}
	}
	return r, nil
}

func wouldBe(a plan.Action) string {
	switch a {
	case plan.Add:
		return "added"
	case plan.Merge:
		return "merged"
	case plan.Overwrite:
		return "overwritten"
	}
	return "skipped"
}

func runOp(p *plan.Plan, op plan.Op, opts *Options, set **backup.Set) (status string, restored bool, err error) {
	status = wouldBe(op.Action)
	if op.Source == nil {
		return status, false, errors.New("operation has no source entry")
	}
	name := op.Source.FileName()
	bucketDir := p.DestBucket.Path
	destPath := filepath.Join(bucketDir, name)
	if op.Dest != nil && op.Dest.Path != "" {
		destPath = op.Dest.Path
	}
	wantID := strings.TrimSuffix(name, ".json")

	// Compute the bytes to write.
	var data, oldData []byte
	switch op.Action {
	case plan.Add:
		if _, statErr := os.Lstat(destPath); statErr == nil {
			return status, false, errors.New("destination file already exists; refusing to overwrite it with an add")
		}
		data, err = merge.PrepareAdd(op.Source.Raw, p.Options.MergePolicy, p.Options.StripAccountConfig)
	case plan.Merge, plan.Overwrite:
		if op.Dest == nil {
			return status, false, errors.New("operation has no destination entry")
		}
		oldData, err = os.ReadFile(destPath)
		if err != nil {
			return status, false, fmt.Errorf("cannot read destination file: %w", err)
		}
		if op.Action == plan.Merge {
			data, err = merge.Merge(op.Source.Raw, oldData, p.Options.MergePolicy)
		} else {
			data = op.Source.Raw
		}
	default:
		return status, false, fmt.Errorf("unsupported action %v", op.Action)
	}
	if err != nil {
		return status, false, fmt.Errorf("cannot prepare new content: %w", err)
	}

	// Destination folder.
	if st, statErr := os.Stat(bucketDir); statErr != nil || !st.IsDir() {
		acct := filepath.Dir(bucketDir)
		if ast, aerr := os.Stat(acct); aerr != nil || !ast.IsDir() {
			return status, false, errors.New("the destination account folder does not exist")
		}
		if !opts.DryRun {
			if err := os.Mkdir(bucketDir, 0o700); err != nil {
				return status, false, fmt.Errorf("cannot create destination folder: %w", err)
			}
		}
	}

	if opts.DryRun {
		return status, false, nil
	}

	mode := fs.FileMode(0o600)
	switch {
	case oldData != nil || op.Dest != nil:
		if st, serr := os.Stat(destPath); serr == nil {
			mode = st.Mode().Perm()
		} else if op.Dest != nil && op.Dest.Mode.Perm() != 0 {
			mode = op.Dest.Mode.Perm()
		}
	case op.Source.Mode.Perm() != 0:
		mode = op.Source.Mode.Perm()
	}

	// Back up first.
	backedUp := false
	if op.Dest != nil {
		if *set == nil {
			if opts.BackupRoot == "" {
				return status, false, errors.New("no backup folder configured; refusing to overwrite without a backup")
			}
			s, berr := backup.Begin(opts.BackupRoot, opts.Note)
			if berr != nil {
				return status, false, fmt.Errorf("cannot create backup: %w", berr)
			}
			*set = s
		}
		if err := (*set).Add(destPath, oldData, p.DestBucket.AccountUUID, p.DestBucket.OrgUUID, op.Source.SessionID); err != nil {
			return status, false, fmt.Errorf("cannot back up destination file: %w", err)
		}
		backedUp = true
	}

	if werr := fsx.WriteAtomic(destPath, data, mode); werr != nil {
		return status, false, fmt.Errorf("write failed: %w", werr)
	}

	verify := opts.verify
	if verify == nil {
		verify = verifyFile
	}
	if verr := verify(destPath, wantID); verr != nil {
		if backedUp {
			if rerr := fsx.WriteAtomic(destPath, oldData, mode); rerr != nil {
				return status, false, fmt.Errorf("verification failed (%v) and restoring the backup also failed: %w", verr, rerr)
			}
			return status, true, fmt.Errorf("verification failed; original restored: %w", verr)
		}
		_ = os.Remove(destPath)
		return status, false, fmt.Errorf("verification failed; new file removed: %w", verr)
	}
	return status, false, nil
}

// verifyFile re-reads path and checks it is a JSON object whose sessionId is
// wantID.
func verifyFile(path, wantID string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-read: %w", err)
	}
	var v struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF}), &v); err != nil {
		return fmt.Errorf("written file does not parse: %w", err)
	}
	if v.SessionID != wantID {
		return errors.New("written file has a different sessionId than its name")
	}
	return nil
}
