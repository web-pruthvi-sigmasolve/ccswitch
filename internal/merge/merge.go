// Package merge implements the field-level merge of Claude Desktop sidebar
// index entries. It is pure: it only transforms bytes and never touches disk.
package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
)

// Policy says which side owns which key when two copies of an entry meet.
type Policy struct {
	// SourceOwned keys are copied from the source on merge.
	SourceOwned []string
	// DestOwned keys are never overwritten. Every key not listed anywhere is
	// also kept from the destination.
	DestOwned []string
	// StripOnAdd keys are replaced with [] when an entry is added and
	// stripping is enabled (account-scoped data such as connectors).
	StripOnAdd []string
}

// DefaultPolicy returns the policy described in PLAN.md section 3.3.
func DefaultPolicy() Policy {
	return Policy{
		SourceOwned: []string{
			"title", "titleSource", "titleTurn", "isArchived", "lastActivityAt",
			"completedTurns", "lastAssistantUuid", "postTurnSummary",
			"postTurnSummaryFor", "latestUserFrameAt", "model", "effort",
			"permissionMode", "cwd", "originCwd", "gitAnchors",
			"gitAnchorsFolderRealpath", "subagentsTruncatedFor",
		},
		DestOwned: []string{
			"lastFocusedAt", "remoteMcpServersConfig", "terminalClaudeTabOrdinal",
			"sessionPermissionUpdates", "alwaysAllowedReasons",
		},
		StripOnAdd: []string{"remoteMcpServersConfig"},
	}
}

type field struct {
	key string
	val json.RawMessage
}

// object is an insertion-ordered JSON object.
type object struct {
	fields []field
	index  map[string]int
}

func (o *object) set(key string, val json.RawMessage) {
	if i, ok := o.index[key]; ok {
		o.fields[i].val = val
		return
	}
	o.index[key] = len(o.fields)
	o.fields = append(o.fields, field{key, val})
}

func (o *object) get(key string) (json.RawMessage, bool) {
	i, ok := o.index[key]
	if !ok {
		return nil, false
	}
	return o.fields[i].val, true
}

var bom = []byte{0xEF, 0xBB, 0xBF}

func parseObject(b []byte) (*object, error) {
	b = bytes.TrimPrefix(b, bom)
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("merge: invalid JSON: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("merge: JSON value is not an object")
	}
	o := &object{index: map[string]int{}}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("merge: invalid JSON: %w", err)
		}
		key, ok := kt.(string)
		if !ok {
			return nil, errors.New("merge: invalid JSON object key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("merge: invalid JSON value for key %q: %w", key, err)
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return nil, fmt.Errorf("merge: invalid JSON value for key %q: %w", key, err)
		}
		o.set(key, buf.Bytes())
	}
	if _, err := dec.Token(); err != nil { // closing brace
		return nil, fmt.Errorf("merge: invalid JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("merge: unexpected data after JSON object")
	}
	return o, nil
}

func (o *object) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range o.fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		var kb bytes.Buffer
		enc := json.NewEncoder(&kb)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(f.key); err != nil {
			return nil, fmt.Errorf("merge: encode key: %w", err)
		}
		buf.Write(bytes.TrimRight(kb.Bytes(), "\n"))
		buf.WriteByte(':')
		buf.Write(f.val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func toSet(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// Merge applies source-owned keys from src onto dst and returns minified
// single-line JSON without a trailing newline. dst's key order is preserved;
// keys that only src has (and that are not destination-owned) are appended in
// src order.
func Merge(src, dst []byte, p Policy) ([]byte, error) {
	so, err := parseObject(src)
	if err != nil {
		return nil, fmt.Errorf("source entry: %w", err)
	}
	do, err := parseObject(dst)
	if err != nil {
		return nil, fmt.Errorf("destination entry: %w", err)
	}
	srcOwned := toSet(p.SourceOwned)
	destOwned := toSet(p.DestOwned)
	for _, f := range so.fields {
		_, inDst := do.get(f.key)
		switch {
		case srcOwned[f.key]:
			do.set(f.key, f.val)
		case destOwned[f.key]:
			// never overwritten, never added
		case !inDst:
			do.set(f.key, f.val) // unknown key only the source has
		}
	}
	return do.marshal()
}

// PrepareAdd returns the bytes to write when an entry is added to a bucket
// that has no copy yet. Without stripping the source bytes are returned
// unchanged apart from a leading BOM. With stripping, every StripOnAdd key
// present is set to [] and the entry is re-emitted minified in its original
// key order.
func PrepareAdd(src []byte, p Policy, stripAccountConfig bool) ([]byte, error) {
	if !stripAccountConfig {
		out := bytes.TrimPrefix(src, bom)
		return append([]byte(nil), out...), nil
	}
	o, err := parseObject(src)
	if err != nil {
		return nil, fmt.Errorf("source entry: %w", err)
	}
	for _, k := range p.StripOnAdd {
		if _, ok := o.get(k); ok {
			o.set(k, json.RawMessage("[]"))
		}
	}
	return o.marshal()
}

func decodeValue(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(raw, bom)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

// Equal reports whether a and b are semantically identical JSON, ignoring
// whitespace and object key order. Invalid JSON is never equal.
func Equal(a, b []byte) bool {
	va, err := decodeValue(a)
	if err != nil {
		return false
	}
	vb, err := decodeValue(b)
	if err != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

// DiffKeys returns the sorted keys whose values differ between two JSON
// objects, including keys present on only one side. It returns nil when
// either input is not a valid JSON object.
func DiffKeys(a, b []byte) []string {
	oa, err := parseObject(a)
	if err != nil {
		return nil
	}
	ob, err := parseObject(b)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range oa.fields {
		v, ok := ob.get(f.key)
		if !ok || !Equal(f.val, v) {
			out = append(out, f.key)
		}
	}
	for _, f := range ob.fields {
		if _, ok := oa.get(f.key); !ok {
			out = append(out, f.key)
		}
	}
	sort.Strings(out)
	return out
}
