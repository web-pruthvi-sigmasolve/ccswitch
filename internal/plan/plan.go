// Package plan classifies the difference between a source and a destination
// bucket and decides, per chat, what to do. It is pure: no IO.
package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ccswitch/ccswitch/internal/merge"
	"github.com/ccswitch/ccswitch/internal/store"
)

// Class describes how a source entry relates to the destination.
type Class int

// Entry classes (PLAN.md section 3.2).
const (
	// Missing means the destination has no file for the chat.
	Missing Class = iota
	// Identical means both files hold the same JSON.
	Identical
	// SourceNewer means the source copy is the more recently used one.
	SourceNewer
	// DestNewer means the destination copy is the more recently used one.
	DestNewer
	// MetaOnly means only destination-owned keys differ.
	MetaOnly
	// Orphan means the source transcript file is missing.
	Orphan
	// Unreadable means the destination file could not be parsed.
	Unreadable
)

// String returns the class name.
func (c Class) String() string {
	switch c {
	case Missing:
		return "Missing"
	case Identical:
		return "Identical"
	case SourceNewer:
		return "SourceNewer"
	case DestNewer:
		return "DestNewer"
	case MetaOnly:
		return "MetaOnly"
	case Orphan:
		return "Orphan"
	case Unreadable:
		return "Unreadable"
	}
	return fmt.Sprintf("Class(%d)", int(c))
}

// Action is what will be done to the destination.
type Action int

// Actions.
const (
	// Add writes a new destination file.
	Add Action = iota
	// Merge applies source-owned fields onto the destination file.
	Merge
	// Overwrite replaces the destination file with the source bytes.
	Overwrite
	// Skip leaves the destination untouched.
	Skip
)

// String returns the action name.
func (a Action) String() string {
	switch a {
	case Add:
		return "Add"
	case Merge:
		return "Merge"
	case Overwrite:
		return "Overwrite"
	case Skip:
		return "Skip"
	}
	return fmt.Sprintf("Action(%d)", int(a))
}

// OnConflict is the policy applied when the destination is newer.
type OnConflict int

// Conflict policies.
const (
	// KeepDest keeps the destination copy (default).
	KeepDest OnConflict = iota
	// PreferSource lets the source win even when the destination is newer.
	PreferSource
	// SkipConflicts skips conflicting chats (CLI name "skip"; named
	// SkipConflicts because Skip is already an Action).
	SkipConflicts
)

// String returns the CLI spelling of the policy.
func (o OnConflict) String() string {
	switch o {
	case KeepDest:
		return "keep-dest"
	case PreferSource:
		return "prefer-source"
	case SkipConflicts:
		return "skip"
	}
	return fmt.Sprintf("OnConflict(%d)", int(o))
}

// ParseOnConflict parses "keep-dest", "prefer-source" or "skip".
func ParseOnConflict(s string) (OnConflict, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "keep-dest", "":
		return KeepDest, nil
	case "prefer-source":
		return PreferSource, nil
	case "skip":
		return SkipConflicts, nil
	}
	return KeepDest, fmt.Errorf("unknown conflict policy %q (use keep-dest, prefer-source or skip)", s)
}

// Mode selects how existing destination files are treated.
type Mode int

// Modes.
const (
	// ModeMerge merges field by field (default).
	ModeMerge Mode = iota
	// ModeOverwrite replaces the destination file byte for byte.
	ModeOverwrite
	// ModeAddOnly never touches existing destination files.
	ModeAddOnly
)

// String returns the CLI spelling of the mode.
func (m Mode) String() string {
	switch m {
	case ModeMerge:
		return "merge"
	case ModeOverwrite:
		return "overwrite"
	case ModeAddOnly:
		return "add-only"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// ParseMode parses "merge", "overwrite" or "add-only".
func ParseMode(s string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "merge", "":
		return ModeMerge, nil
	case "overwrite":
		return ModeOverwrite, nil
	case "add-only":
		return ModeAddOnly, nil
	}
	return ModeMerge, fmt.Errorf("unknown mode %q (use merge, overwrite or add-only)", s)
}

// Options control Build.
type Options struct {
	Mode       Mode
	OnConflict OnConflict
	// IncludeOrphans also processes chats whose transcript is missing.
	IncludeOrphans bool
	// StripAccountConfig replaces account-scoped keys with [] on Add.
	StripAccountConfig bool
	// MergePolicy is the field ownership policy.
	MergePolicy merge.Policy
	// Overrides maps a sessionId to a user-chosen action.
	Overrides map[string]Action
	// DestAccountBuckets are all buckets of the destination account. Optional:
	// when set, a chat that is missing in the destination bucket but present in
	// another of these buckets is reported as such (Op.AlsoInOrg). Nil keeps
	// the plain behaviour.
	DestAccountBuckets []*store.Bucket
}

// DefaultOptions returns the default options (merge, keep-dest, strip on).
func DefaultOptions() Options {
	return Options{StripAccountConfig: true, MergePolicy: merge.DefaultPolicy()}
}

// Op is the decision for one chat.
type Op struct {
	Source *store.Entry
	// Dest is nil when the chat is Missing at the destination.
	Dest   *store.Entry
	Class  Class
	Action Action
	// Reason is one plain sentence shown to the user.
	Reason string
	// DiffKeys are the top-level keys whose values differ. Destination-owned
	// keys are left out when the chosen action can never change them.
	DiffKeys []string
	// AlsoInOrg is the org UUID of another bucket of the destination account
	// that already holds this chat (only set for Missing chats).
	AlsoInOrg string
	// CrossOrg is true when source and destination are different orgs.
	CrossOrg bool
}

// Plan is the full set of decisions for one source to destination move.
type Plan struct {
	SourceBucket, DestBucket *store.Bucket
	Ops                      []Op
	// Options are the options the plan was built with; apply needs the
	// merge policy and the strip flag.
	Options Options

	Adds, Merges, Overwrites, Skips, Conflicts, Orphans int
}

// Changes returns the ops that will write something.
func (p *Plan) Changes() []Op {
	var out []Op
	for _, op := range p.Ops {
		if op.Action != Skip {
			out = append(out, op)
		}
	}
	return out
}

// Summary renders the counts on one line.
func (p *Plan) Summary() string {
	parts := []string{
		fmt.Sprintf("%d to add", p.Adds),
		fmt.Sprintf("%d to merge", p.Merges),
	}
	if p.Overwrites > 0 {
		parts = append(parts, fmt.Sprintf("%d to overwrite", p.Overwrites))
	}
	parts = append(parts,
		fmt.Sprintf("%d kept (destination newer)", p.Conflicts),
		fmt.Sprintf("%d orphans skipped", p.Orphans),
	)
	return strings.Join(parts, " · ")
}

// Build classifies every selected source entry against dst and returns the
// plan. Op order follows selected.
func Build(src, dst *store.Bucket, selected []*store.Entry, opts Options) (*Plan, error) {
	if src == nil || dst == nil {
		return nil, errors.New("plan: source and destination buckets are required")
	}
	if src == dst || (src.Path != "" && src.Path == dst.Path) {
		return nil, errors.New("plan: source and destination are the same bucket")
	}
	unreadable := map[string]bool{}
	for _, u := range dst.Unreadable {
		unreadable[baseName(u.Path)] = true
	}
	p := &Plan{SourceBucket: src, DestBucket: dst, Options: opts}
	crossOrg := src.OrgUUID != dst.OrgUUID
	for _, e := range selected {
		if e == nil || src.Find(e.SessionID) == nil {
			id := ""
			if e != nil {
				id = e.SessionID
			}
			return nil, fmt.Errorf("plan: entry %q does not belong to the source bucket", id)
		}
		op := classify(e, dst, unreadable, opts)
		op.CrossOrg = crossOrg
		p.Ops = append(p.Ops, op)
		switch op.Action {
		case Add:
			p.Adds++
		case Merge:
			p.Merges++
		case Overwrite:
			p.Overwrites++
		default:
			p.Skips++
			if op.Class == DestNewer {
				p.Conflicts++
			}
			if op.Class == Orphan {
				p.Orphans++
			}
		}
	}
	return p, nil
}

func baseName(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	return path[i+1:]
}

func classify(e *store.Entry, dst *store.Bucket, unreadable map[string]bool, opts Options) Op {
	op := Op{Source: e, Action: Skip}
	if unreadable[e.FileName()] {
		op.Class = Unreadable
		op.Reason = "destination file is unreadable"
		return op
	}
	d := dst.Find(e.SessionID)
	op.Dest = d
	orphan := !e.TranscriptExists
	orphanBlocked := orphan && !opts.IncludeOrphans

	switch {
	case d == nil:
		op.Class = Missing
		op.Action = Add
		op.Reason = "not in the destination yet"
		for _, ob := range opts.DestAccountBuckets {
			if ob == nil || ob == dst || (ob.Path != "" && ob.Path == dst.Path) {
				continue
			}
			if ob.Find(e.SessionID) != nil {
				op.AlsoInOrg = ob.OrgUUID
				op.Reason = fmt.Sprintf("not in this org yet; already in this account's %s… bucket", shortOrg(ob.OrgUUID))
				break
			}
		}
	case merge.Equal(e.Raw, d.Raw):
		op.Class = Identical
		op.Reason = "already identical in the destination"
	default:
		op.DiffKeys = merge.DiffKeys(e.Raw, d.Raw)
		classifyExisting(&op, e, d, opts)
		if op.Action == Merge || (opts.Mode == ModeMerge && (op.Class == SourceNewer || op.Class == DestNewer)) {
			op.DiffKeys = changedByMerge(op.DiffKeys, e.Raw, d.Raw, opts.MergePolicy)
		}
	}

	if orphan {
		if orphanBlocked {
			op.Class = Orphan
			op.Action = Skip
			op.Reason = "transcript file is missing; skipped (orphan)"
		} else if op.Action != Skip {
			op.Reason += " (orphan: transcript file is missing)"
		}
	}

	if ov, ok := opts.Overrides[e.SessionID]; ok {
		applyOverride(&op, ov, orphanBlocked)
	}
	return op
}

func classifyExisting(op *Op, e, d *store.Entry, opts Options) {
	sa, da := e.Activity(), d.Activity()
	switch {
	case sa > da:
		op.Class = SourceNewer
	case da > sa:
		op.Class = DestNewer
	default:
		if onlyDestOwned(op.DiffKeys, opts.MergePolicy) {
			op.Class = MetaOnly
			op.Reason = "same activity; only destination-specific settings differ"
			return
		}
		if e.LastFocusedAt >= d.LastFocusedAt {
			op.Class = SourceNewer
		} else {
			op.Class = DestNewer
		}
	}
	existing := func() Action {
		switch opts.Mode {
		case ModeOverwrite:
			return Overwrite
		case ModeAddOnly:
			return Skip
		}
		return Merge
	}
	switch op.Class {
	case SourceNewer:
		op.Action = existing()
		switch op.Action {
		case Skip:
			op.Reason = "source is newer, but add-only mode never touches existing chats"
		case Overwrite:
			op.Reason = "source is newer; destination file will be replaced"
		default:
			op.Reason = "source is newer; its fields will be merged into the destination"
		}
	case DestNewer:
		switch opts.OnConflict {
		case PreferSource:
			op.Action = existing()
			if op.Action == Skip {
				op.Reason = "destination is newer; add-only mode keeps it"
			} else {
				op.Reason = "destination is newer, but you chose to prefer the source"
			}
		case SkipConflicts:
			op.Reason = "destination is newer; skipped by policy"
		default:
			op.Reason = "destination is newer; kept as is"
		}
	}
}

func shortOrg(u string) string {
	if len(u) > 8 {
		return u[:8]
	}
	return u
}

// changedByMerge narrows the differing keys to those merge.Merge would really
// change: keys the source has that are source-owned, or that only the source
// has and are not destination-owned. It returns nil when nothing is left.
func changedByMerge(keys []string, srcRaw, dstRaw []byte, p merge.Policy) []string {
	srcKeys, dstKeys := topKeys(srcRaw), topKeys(dstRaw)
	srcOwned, destOwned := map[string]bool{}, map[string]bool{}
	for _, k := range p.SourceOwned {
		srcOwned[k] = true
	}
	for _, k := range p.DestOwned {
		destOwned[k] = true
	}
	var out []string
	for _, k := range keys {
		if !srcKeys[k] {
			continue
		}
		if srcOwned[k] || (!destOwned[k] && !dstKeys[k]) {
			out = append(out, k)
		}
	}
	return out
}

// topKeys returns the set of top-level keys of a JSON object (empty on error).
func topKeys(raw []byte) map[string]bool {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}), &m)
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func onlyDestOwned(keys []string, p merge.Policy) bool {
	own := map[string]bool{}
	for _, k := range p.DestOwned {
		own[k] = true
	}
	for _, k := range keys {
		if !own[k] {
			return false
		}
	}
	return true
}

func applyOverride(op *Op, ov Action, orphanBlocked bool) {
	if ov != Skip && orphanBlocked {
		return
	}
	switch ov {
	case Skip:
		op.Action = Skip
		op.Reason = "skipped by your choice"
		return
	case Add, Merge, Overwrite:
		if op.Dest == nil {
			ov = Add
		} else if ov == Add {
			ov = Merge
		}
		op.Action = ov
		op.Reason = "chosen by you: " + strings.ToLower(ov.String())
	}
}
