package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/ccswitch/ccswitch/internal/app"
)

// style is an ANSI text style.
type style int

const (
	styleNone style = iota
	styleBold
	styleDim
	styleRed
	styleGreen
	styleYellow
	styleCyan
)

var ansiCodes = map[style]string{
	styleBold:   "1",
	styleDim:    "2",
	styleRed:    "31",
	styleGreen:  "32",
	styleYellow: "33",
	styleCyan:   "36",
}

// ui renders text for one output stream: optional colour and glyph set.
type ui struct {
	w     io.Writer
	color bool
	ascii bool
}

// paint wraps s in the ANSI sequence for st when colour is on.
func (u *ui) paint(st style, s string) string {
	if !u.color || st == styleNone {
		return s
	}
	return "\x1b[" + ansiCodes[st] + "m" + s + "\x1b[0m"
}

// Glyph kinds.
const (
	glyphAdd   = "add"
	glyphMerge = "merge"
	glyphWarn  = "warn"
	glyphSame  = "same"
	glyphOK    = "ok"
	glyphFail  = "fail"
)

// glyph returns the coloured symbol for a kind, with an ASCII fallback.
func (u *ui) glyph(kind string) string {
	var sym string
	var st style
	switch kind {
	case glyphAdd:
		sym, st = pick(u.ascii, "+", "✚"), styleGreen
	case glyphMerge:
		sym, st = pick(u.ascii, "~", "↻"), styleCyan
	case glyphWarn:
		sym, st = pick(u.ascii, "!", "⚠"), styleYellow
	case glyphSame:
		sym, st = pick(u.ascii, "=", "≡"), styleDim
	case glyphOK:
		sym, st = pick(u.ascii, "ok", "✓"), styleGreen
	case glyphFail:
		sym, st = pick(u.ascii, "x", "✗"), styleRed
	default:
		sym = "?"
	}
	return u.paint(st, sym)
}

func pick(ascii bool, a, uni string) string {
	if ascii {
		return a
	}
	return uni
}

// sep is the separator used in summary lines.
func (u *ui) sep() string { return pick(u.ascii, "-", "·") }

// ellipsis returns "..." or "…".
func (u *ui) ellipsis() string { return pick(u.ascii, "...", "…") }

// table prints rows of two columns, padding the first to the widest key.
func (u *ui) table(rows [][2]string) {
	width := 0
	for _, r := range rows {
		if n := utf8.RuneCountInString(r[0]); n > width {
			width = n
		}
	}
	for _, r := range rows {
		pad := strings.Repeat(" ", width-utf8.RuneCountInString(r[0]))
		fmt.Fprintf(u.w, "  %s%s  %s\n", u.paint(styleBold, r[0]), pad, r[1])
	}
}

// printJSON writes v as indented JSON.
func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// confirm asks a yes/no question on stdin. With assumeYes it returns true
// without asking. When stdin is not a terminal it refuses with an error rather
// than guessing.
func (e *env) confirm(prompt string, assumeYes bool) (bool, error) {
	if assumeYes {
		return true, nil
	}
	if !e.stdinTTY {
		return false, &app.HintError{
			Msg:  "Refusing to change anything without confirmation because input is not a terminal.",
			Hint: "Pass --yes to confirm, or --dry-run to only see the plan.",
		}
	}
	fmt.Fprintf(e.out, "%s [y/N] ", prompt)
	if e.inr == nil {
		e.inr = bufio.NewReader(e.in)
	}
	line, err := e.inr.ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}
