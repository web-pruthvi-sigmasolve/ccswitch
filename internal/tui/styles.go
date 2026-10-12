package tui

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// glyphs is the symbol set used by every screen. The ASCII variant only uses
// 7-bit characters so legacy consoles render it correctly.
type glyphs struct {
	add, merge, conflict, same, orphan string
	collapsed, expanded                string
	checkOn, checkOff, checkPart       string
	arrow, ellipsis, sep, rule, cursor string
	restored                           string
}

func unicodeGlyphs() glyphs {
	return glyphs{
		add: "✓", merge: "↑", conflict: "⚠", same: "·", orphan: "✗", restored: "↺",
		collapsed: "▸", expanded: "▾",
		checkOn: "[x]", checkOff: "[ ]", checkPart: "[-]",
		arrow: "→", ellipsis: "…", sep: "·", rule: "─", cursor: ">",
	}
}

func asciiGlyphs() glyphs {
	return glyphs{
		add: "+", merge: "~", conflict: "!", same: "=", orphan: "x", restored: "r",
		collapsed: ">", expanded: "v",
		checkOn: "[x]", checkOff: "[ ]", checkPart: "[-]",
		arrow: "->", ellipsis: "...", sep: "|", rule: "-", cursor: ">",
	}
}

// styles bundles the lipgloss styles and the glyph set of one program.
type styles struct {
	ascii bool
	g     glyphs

	title, dim, accent, ok, warn, bad, bold, cursorRow lipgloss.Style
}

// detectASCII decides whether to use ASCII glyphs: when forced, when TERM is
// dumb, or on a legacy Windows console (no Windows Terminal, no ConEmu ANSI).
func detectASCII(goos string, getenv func(string) string, forced bool) bool {
	if forced || getenv("TERM") == "dumb" {
		return true
	}
	return goos == "windows" && getenv("WT_SESSION") == "" && getenv("ConEmuANSI") != "ON"
}

func newStyles(ascii, noColor bool) styles {
	r := lipgloss.NewRenderer(os.Stdout)
	if noColor || os.Getenv("NO_COLOR") != "" {
		r.SetColorProfile(termenv.Ascii)
	}
	col := func(light, dark string) lipgloss.AdaptiveColor {
		return lipgloss.AdaptiveColor{Light: light, Dark: dark}
	}
	s := styles{ascii: ascii, g: unicodeGlyphs()}
	if ascii {
		s.g = asciiGlyphs()
	}
	s.title = r.NewStyle().Bold(true).Foreground(col("#5B3FD0", "#B8A6FF"))
	s.dim = r.NewStyle().Foreground(col("#6B6B6B", "#8A8A8A"))
	s.accent = r.NewStyle().Foreground(col("#0B6FA4", "#6CC4F0"))
	s.ok = r.NewStyle().Foreground(col("#1A7F37", "#5FD068"))
	s.warn = r.NewStyle().Foreground(col("#9A6700", "#E6B450"))
	s.bad = r.NewStyle().Foreground(col("#C62828", "#FF7B72"))
	s.bold = r.NewStyle().Bold(true)
	s.cursorRow = r.NewStyle().Bold(true)
	return s
}

// text makes arbitrary text safe for the terminal: control characters become
// spaces and, in ASCII mode, every non-ASCII rune is transliterated or
// replaced by "?".
func (s styles) text(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r == '\t' || unicode.IsControl(r):
			b.WriteByte(' ')
		case s.ascii && r > 127:
			switch r {
			case '…':
				b.WriteString("...")
			case '·':
				b.WriteString("|")
			case '→':
				b.WriteString("->")
			case '—', '–':
				b.WriteString("-")
			case '‘', '’':
				b.WriteString("'")
			case '“', '”':
				b.WriteString("\"")
			default:
				b.WriteByte('?')
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fit truncates plain text to w display cells, ending with the ellipsis.
func (s styles) fit(text string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s.text(text), w, s.g.ellipsis)
}

// fitTail keeps the end of a long path, dropping the start behind an ellipsis.
func (s styles) fitTail(text string, w int) string {
	text = s.text(text)
	if w <= 0 || lipgloss.Width(text) <= w {
		return text
	}
	rs := []rune(text)
	for len(rs) > 0 && lipgloss.Width(string(rs))+lipgloss.Width(s.g.ellipsis) > w {
		rs = rs[1:]
	}
	return s.g.ellipsis + string(rs)
}

// wrap word-wraps text to w cells.
func (s styles) wrap(text string, w int) string { return ansi.Wrap(s.text(text), max(w, 10), "") }

// padTo right-pads s with spaces to w display cells.
func padTo(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// finalize is the last safety net over a whole view in ASCII mode.
func (s styles) finalize(view string) string {
	if !s.ascii {
		return view
	}
	return strings.Map(func(r rune) rune {
		if r > 127 {
			return '?'
		}
		return r
	}, view)
}

// ruleLine draws a horizontal rule of w cells.
func (s styles) ruleLine(w int) string {
	return s.dim.Render(strings.Repeat(s.g.rule, max(w, 0)))
}

// relTime renders a coarse relative time such as "2h ago".
func relTime(now, t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < 0, d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%dw ago", int(d/(7*24*time.Hour)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo ago", int(d/(30*24*time.Hour)))
	}
	return fmt.Sprintf("%dy ago", int(d/(365*24*time.Hour)))
}

// pluralize returns "1 chat" / "2 chats".
func pluralize(n int, one string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %ss", n, one)
}
