// Package tui is the terminal interface: a file browser beside Info, Tracks,
// Metadata and Convert panels, built on the same core packages as the CLI.
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Mimic890/hypr-encode/pkg/meta"
	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

type tab int

const (
	tabInfo tab = iota
	tabTracks
	tabMeta
	tabConvert
)

var tabNames = []string{"Info", "Tracks", "Metadata", "Convert"}

var (
	accent    = lipgloss.Color("#2dc8b0")
	dim       = lipgloss.Color("#6c7680")
	warn      = lipgloss.Color("#e05050")
	good      = lipgloss.Color("#64dc64")
	boldStyle = lipgloss.NewStyle().Bold(true)
	dimStyle  = lipgloss.NewStyle().Foreground(dim)
	headStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	errStyle  = lipgloss.NewStyle().Foreground(warn)
	okStyle   = lipgloss.NewStyle().Foreground(good)
	cursorSty = lipgloss.NewStyle().Reverse(true)
)

// Global key bindings; panels add their own in helpKeys.
var (
	keyQuit  = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit"))
	keyTabs  = key.NewBinding(key.WithKeys("1", "2", "3", "4"), key.WithHelp("1-4", "tabs"))
	keyFocus = key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "focus"))
	keyUp    = key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up"))
	keyDown  = key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down"))
	keyOpen  = key.NewBinding(key.WithKeys("enter", "right", "l"), key.WithHelp("enter", "open"))
	keyBack  = key.NewBinding(key.WithKeys("backspace", "left", "h"), key.WithHelp("⌫", "parent"))
	keyMark  = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "mark"))
)

// fileState is what was loaded for one file.
type fileState struct {
	info *probe.MediaInfo
	meta *meta.FileMeta
	err  error
}

type loadedMsg struct {
	path string
	st   *fileState
}

// promptKind says what the shared text input is collecting.
type promptKind int

const (
	promptNone promptKind = iota
	promptTagValue
	promptTagAdd
	promptField
	promptPresetName
)

type model struct {
	ctx           context.Context
	width, height int
	tab           tab
	panelFocus    bool
	scroll        int
	quitting      bool

	browser browser
	files   map[string]*fileState
	et      *meta.ExifTool

	input     textinput.Model
	prompt    promptKind
	promptKey string // tag key or field index the prompt edits

	status    string
	statusErr bool
	help      help.Model

	meta metaEditor
	conv *convertPanel
}

// Run starts the TUI in dir and blocks until the user quits.
func Run(ctx context.Context, dir string) error {
	m, err := newModel(ctx, dir)
	if err != nil {
		return err
	}
	if m.et != nil {
		defer m.et.Close()
	}
	_, err = tea.NewProgram(m, tea.WithContext(ctx)).Run()
	return err
}

func newModel(ctx context.Context, dir string) (model, error) {
	m := model{ctx: ctx, files: map[string]*fileState{}, help: help.New(), input: textinput.New()}
	if err := m.browser.load(dir); err != nil {
		return m, err
	}
	if _, err := tools.Path("exiftool"); err == nil {
		m.et, _ = meta.StartExifTool(ctx) // without it images show ffprobe tags
	}
	m.conv = newConvertPanel(dir)
	return m, nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.conv.loadPresets(m.ctx), m.loadCurrent())
}

// loadCurrent probes the file under the cursor unless it is cached.
func (m model) loadCurrent() tea.Cmd {
	path, isDir := m.browser.current()
	if path == "" || isDir || m.files[path] != nil {
		return nil
	}
	ctx, et := m.ctx, m.et
	return func() tea.Msg {
		st := &fileState{}
		pctx, cancel := context.WithTimeout(ctx, probe.DefaultTimeout)
		st.info, st.err = probe.ScanFileContext(pctx, path)
		cancel()
		if fm, err := meta.Read(ctx, path, et); err == nil {
			st.meta, st.err = fm, nil // exiftool may read images ffprobe cannot
		} else if st.err == nil {
			st.err = err
		}
		return loadedMsg{path, st}
	}
}

func (m *model) setStatus(err error, format string, a ...any) {
	m.statusErr = err != nil
	m.status = fmt.Sprintf(format, a...)
	if err != nil {
		m.status = err.Error()
	}
}

func (m model) currentState() *fileState {
	path, isDir := m.browser.current()
	if isDir {
		return nil
	}
	return m.files[path]
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.SetWidth(msg.Width)
		return m, nil
	case loadedMsg:
		m.files[msg.path] = msg.st
		return m, nil
	case presetsMsg, planMsg, jobEventMsg, queueDoneMsg:
		cmd := m.conv.update(msg, &m)
		if _, done := msg.(queueDoneMsg); done && m.quitting {
			return m, tea.Quit
		}
		return m, cmd
	case metaWrittenMsg:
		return m.metaWritten(msg)
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.prompt != promptNone {
		return m.handlePrompt(msg)
	}
	if m.meta.confirm {
		return m.handleConfirm(msg)
	}
	switch {
	case key.Matches(msg, keyQuit):
		if m.conv.running {
			m.quitting = true
			m.conv.cancel()
			m.setStatus(nil, "stopping jobs…")
			return m, nil
		}
		return m, tea.Quit
	case key.Matches(msg, keyTabs):
		m.tab, m.scroll = tab(msg.String()[0]-'1'), 0
		return m, nil
	case key.Matches(msg, keyFocus):
		m.panelFocus = !m.panelFocus
		return m, nil
	}
	if !m.panelFocus {
		return m.handleBrowserKey(msg)
	}
	switch m.tab {
	case tabMeta:
		return m.handleMetaKey(msg)
	case tabConvert:
		return m, m.conv.handleKey(msg, &m)
	}
	switch {
	case key.Matches(msg, keyUp):
		m.scroll = max(m.scroll-1, 0)
	case key.Matches(msg, keyDown):
		m.scroll++
	}
	return m, nil
}

func (m model) handleBrowserKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	b := &m.browser
	switch {
	case key.Matches(msg, keyUp):
		b.move(-1)
	case key.Matches(msg, keyDown):
		b.move(1)
	case msg.String() == "pgup":
		b.move(-10)
	case msg.String() == "pgdown":
		b.move(10)
	case key.Matches(msg, keyOpen):
		if path, isDir := b.current(); isDir {
			if err := b.load(path); err != nil {
				m.setStatus(err, "")
			}
		}
	case key.Matches(msg, keyBack):
		if err := b.load(filepath.Dir(b.dir)); err != nil {
			m.setStatus(err, "")
		}
	case key.Matches(msg, keyMark):
		b.toggleMark()
	default:
		return m, nil
	}
	m.scroll = 0
	m.meta = metaEditor{} // pending edits belong to the file they were made on
	return m, m.loadCurrent()
}

// startPrompt focuses the shared text input.
func (m *model) startPrompt(kind promptKind, keyName, label, value string) tea.Cmd {
	m.prompt, m.promptKey = kind, keyName
	m.input.Prompt = label + ": "
	m.input.SetValue(value)
	m.input.CursorEnd()
	return m.input.Focus()
}

func (m model) handlePrompt(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.prompt = promptNone
		m.input.Blur()
		return m, nil
	case "enter":
		kind, value := m.prompt, m.input.Value()
		m.prompt = promptNone
		m.input.Blur()
		switch kind {
		case promptTagValue, promptTagAdd:
			m.addTagEdit(kind, value)
		case promptField, promptPresetName:
			cmd := m.conv.submitPrompt(kind, m.promptKey, value, &m)
			return m, cmd
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// ── Layout ────────────────────────────────────────────────────────────────────

func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	return v
}

func (m model) render() string {
	if m.width < 50 || m.height < 12 {
		return ansi.Wrap(fmt.Sprintf("Terminal too small (%dx%d); need at least 50x12.", m.width, m.height), max(m.width, 1), " ")
	}
	leftW := min(max(m.width/3, 24), 40)
	rightW := m.width - leftW
	bodyH := m.height - 2

	left := box(m.browser.lines(leftW-2, bodyH-4), "Files", leftW, bodyH, !m.panelFocus)
	right := box(m.panelLines(rightW-2, bodyH-4), m.tabBar(), rightW, bodyH, m.panelFocus)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	status := dimStyle.Render(m.status)
	switch {
	case m.prompt != promptNone:
		status = m.input.View()
	case m.statusErr:
		status = errStyle.Render(m.status)
	}
	return body + "\n" + fitLine(status, m.width) + "\n" + fitLine(m.help.ShortHelpView(m.helpKeys()), m.width)
}

func (m model) tabBar() string {
	parts := make([]string, len(tabNames))
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if tab(i) == m.tab {
			parts[i] = headStyle.Render(label)
		} else {
			parts[i] = dimStyle.Render(label)
		}
	}
	return strings.Join(parts, dimStyle.Render(" │ "))
}

func (m model) panelLines(w, h int) []string {
	st := m.currentState()
	switch m.tab {
	case tabConvert:
		return m.conv.lines(w, h, &m)
	case tabMeta:
		if st != nil && st.meta != nil {
			return m.metaLines(st, w, h)
		}
	}
	switch {
	case st == nil:
		path, isDir := m.browser.current()
		if isDir || path == "" {
			return []string{dimStyle.Render("Select a media file.")}
		}
		return []string{dimStyle.Render("Loading…")}
	case st.err != nil:
		return []string{errStyle.Render(st.err.Error())}
	}
	var lines []string
	if m.tab == tabTracks {
		lines = tracksLines(st)
	} else {
		lines = infoLines(st)
	}
	return lines[min(m.scroll, max(len(lines)-1, 0)):]
}

func (m model) helpKeys() []key.Binding {
	keys := []key.Binding{keyQuit, keyTabs, keyFocus}
	if !m.panelFocus {
		return append(keys, keyUp, keyDown, keyOpen, keyBack, keyMark)
	}
	switch m.tab {
	case tabMeta:
		return append(keys, m.metaKeys()...)
	case tabConvert:
		return append(keys, m.conv.keys()...)
	}
	return append(keys, keyUp, keyDown)
}

// box draws a rounded border of exactly w×h cells around a title line and content.
func box(lines []string, title string, w, h int, active bool) string {
	color := dim
	if active {
		color = accent
	}
	content := append([]string{title, dimStyle.Render(strings.Repeat("─", max(w-2, 0)))}, lines...)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Render(fit(content, w-2, h-2))
}

// fit truncates or pads every line to width w and the block to exactly h lines.
func fit(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = fitLine(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

func fitLine(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

// window returns the slice of lines of height h that keeps line cursor visible.
func window(lines []string, cursor, h int) []string {
	start := 0
	if cursor >= h {
		start = cursor - h + 1
	}
	return lines[min(start, len(lines)):]
}
