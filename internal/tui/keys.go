package tui

import "github.com/charmbracelet/bubbles/key"

// Key bindings shared by the screens.
var (
	keyUp       = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("up/k", "up"))
	keyDown     = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("down/j", "down"))
	keyMove     = key.NewBinding(key.WithKeys("up", "down", "j", "k"), key.WithHelp("up/down", "move"))
	keyPgUp     = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "page up"))
	keyPgDown   = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "page down"))
	keyEnter    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "next"))
	keyBack     = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
	keyQuit     = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit"))
	keyHelp     = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help"))
	keyLabel    = key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "label"))
	keyToggle   = key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle"))
	keyAll      = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all"))
	keyNone     = key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "none"))
	keyFilter   = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	keyCollapse = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "collapse project"))
	keyCycleRow = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "conflict choice"))
	keyNextGrp  = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next project"))
	keyPrevGrp  = key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev project"))
	keyMode     = key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "mode"))
	keyPolicy   = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "conflict policy"))
	keyOrphans  = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "orphans"))
	keySkipped  = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "skipped"))
	keyApply    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "apply"))
	keyRestart  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "start again"))
	keySave     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "save"))
	keyCancel   = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel"))
	keyChoose   = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))
)

// helpKeys is a help.KeyMap built from plain slices, one per screen.
type helpKeys struct {
	short []key.Binding
	full  [][]key.Binding
}

// ShortHelp implements help.KeyMap.
func (h helpKeys) ShortHelp() []key.Binding { return h.short }

// FullHelp implements help.KeyMap.
func (h helpKeys) FullHelp() [][]key.Binding { return h.full }

// helper is implemented by screens that provide their own help bar.
type helper interface{ helpKeys() helpKeys }

// capturer is implemented by screens with a focused text input; the root then
// leaves "?" and other printable keys to the screen.
type capturer interface{ capturing() bool }

func accountsKeys() helpKeys {
	return helpKeys{
		short: []key.Binding{keyMove, keyChoose, keyLabel, keyHelp, keyQuit},
		full:  [][]key.Binding{{keyUp, keyDown}, {keyChoose, keyLabel}, {keyHelp, keyQuit}},
	}
}

func directionKeys() helpKeys {
	return helpKeys{
		short: []key.Binding{keyMove, keyChoose, keyBack, keyHelp, keyQuit},
		full:  [][]key.Binding{{keyUp, keyDown}, {keyChoose, keyBack}, {keyHelp, keyQuit}},
	}
}

func pickKeys() helpKeys {
	return helpKeys{
		short: []key.Binding{keyToggle, keyAll, keyNone, keyFilter, keyCollapse, keyEnter, keyBack, keyHelp},
		full: [][]key.Binding{
			{keyUp, keyDown, keyPgUp, keyPgDown},
			{keyToggle, keyAll, keyNone, keyFilter},
			{keyCollapse, keyNextGrp, keyPrevGrp, keyCycleRow},
			{keyEnter, keyBack, keyHelp, keyQuit},
		},
	}
}

func reviewKeys() helpKeys {
	return helpKeys{
		short: []key.Binding{keyApply, keyMode, keyPolicy, keyOrphans, keySkipped, keyBack, keyHelp},
		full: [][]key.Binding{
			{keyUp, keyDown, keyPgUp, keyPgDown},
			{keyMode, keyPolicy, keyOrphans, keySkipped},
			{keyApply, keyBack, keyHelp, keyQuit},
		},
	}
}

func applyKeys(running bool) helpKeys {
	if running {
		k := []key.Binding{key.NewBinding(key.WithKeys(), key.WithHelp("ctrl+c", "quit when finished"))}
		return helpKeys{short: k, full: [][]key.Binding{k}}
	}
	return helpKeys{short: []key.Binding{keyBack}, full: [][]key.Binding{{keyBack}}}
}

func doneKeys() helpKeys {
	quit := key.NewBinding(key.WithKeys("enter", "q"), key.WithHelp("enter/q", "quit"))
	return helpKeys{short: []key.Binding{quit, keyRestart, keyHelp}, full: [][]key.Binding{{quit, keyRestart, keyHelp}}}
}

func labelKeys() helpKeys {
	return helpKeys{short: []key.Binding{keySave, keyCancel}, full: [][]key.Binding{{keySave, keyCancel}}}
}
