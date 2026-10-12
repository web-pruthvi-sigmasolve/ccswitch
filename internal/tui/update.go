package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// UpdateStatus mirrors update.Status; the tui package never imports the
// update package and has no network code.
type UpdateStatus struct {
	Current, Latest string
	Available       bool
	URL             string
}

// UpdateSource supplies the "new version available" information. Cached must
// return immediately; Result may wait up to the given duration.
type UpdateSource interface {
	Cached() (UpdateStatus, bool)
	Result(wait time.Duration) (UpdateStatus, bool)
}

// updateWait is how long the background command waits for a fresh result.
const updateWait = 3 * time.Second

// updateMsg carries the result of the background update check.
type updateMsg struct{ s UpdateStatus }

var keyUpdate = key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "update info"))

// initUpdate reads the cached status and returns the background command.
func (sh *shared) initUpdate() tea.Cmd {
	src := sh.d.Update
	if src == nil {
		return nil
	}
	if s, ok := src.Cached(); ok && s.Available {
		sh.update = &s
	}
	return func() tea.Msg {
		if s, ok := src.Result(updateWait); ok {
			return updateMsg{s}
		}
		return nil
	}
}

// setUpdate records a status delivered by the background check.
func (sh *shared) setUpdate(s UpdateStatus) {
	if !s.Available {
		return
	}
	sh.update = &s
}

// updateLines is how many lines the update notice (or detail box) currently
// occupies above the help bar for the given screen.
func (m Model) updateLines() int {
	if m.sh.update == nil || m.hidesUpdate() {
		return 0
	}
	if m.sh.updateOpen {
		return 3
	}
	return 1
}

// hidesUpdate reports whether the current screen never shows the notice.
func (m Model) hidesUpdate() bool {
	_, apply := m.cur.(*applyScreen)
	return apply
}

// updateKeysScreen reports whether the current screen offers the "u" key.
func (m Model) updateKeysScreen() bool {
	switch s := m.cur.(type) {
	case *accountsScreen:
		return len(s.sh.store.Accounts) >= 2
	case *doneScreen:
		return true
	}
	return false
}

// handleUpdateKey toggles the detail box. It reports whether it consumed k.
func (m Model) handleUpdateKey(k string) bool {
	sh := m.sh
	if sh.update == nil || !m.updateKeysScreen() {
		return false
	}
	switch {
	case k == "u":
		sh.updateOpen = !sh.updateOpen
		return true
	case k == "esc" && sh.updateOpen:
		sh.updateOpen = false
		return true
	}
	return false
}

// updateView renders the notice line or the detail box ("" when hidden).
func (m Model) updateView() string {
	sh, st := m.sh, m.sh.st
	if m.updateLines() == 0 {
		return ""
	}
	u := sh.update
	w := sh.width
	if sh.updateOpen {
		url := u.URL
		if url == "" {
			url = "(see the ccswitch releases page)"
		}
		lines := []string{
			"Release: " + url,
			"Update with: ccswitch update",
			"Closes with u or esc; hidden until the next version.",
		}
		var out []string
		for _, l := range lines {
			out = append(out, st.dim.Render(st.fit(l, w)))
		}
		return strings.Join(out, "\n")
	}
	arrow, sep := "↑", "·"
	if st.ascii {
		arrow, sep = "^", "-"
	}
	text := arrow + " ccswitch " + u.Latest + " available " + sep + " run: ccswitch update"
	return st.dim.Render(st.fit(text, w))
}

// withUpdateKey appends the "u" binding to a help bar.
func withUpdateKey(h helpKeys, on bool) helpKeys {
	if !on {
		return h
	}
	h.short = append(append([]key.Binding{}, h.short...), keyUpdate)
	full := make([][]key.Binding, len(h.full))
	copy(full, h.full)
	if len(full) > 0 {
		last := len(full) - 1
		full[last] = append(append([]key.Binding{}, full[last]...), keyUpdate)
	}
	h.full = full
	return h
}
