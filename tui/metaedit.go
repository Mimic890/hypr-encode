package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
	"github.com/Mimic890/hypr-encode/pkg/meta"
)

var (
	keyEditTag = key.NewBinding(key.WithKeys("enter", "e"), key.WithHelp("enter", "edit"))
	keyAddTag  = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add"))
	keyDelTag  = key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete"))
	keyDefault = key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "default"))
	keyForced  = key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "forced"))
	keyReset   = key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "undo all"))
	keyWrite   = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "review & write"))
)

// metaRow is one line of the editor: a section header (key == "") or a tag.
type metaRow struct {
	target string // "" for the container (and image tags), else a track selector
	key    string
	value  string
	header string
	track  *meta.Track
}

// metaEditor collects pending changes per target and shows them before writing.
type metaEditor struct {
	cursor       int
	edits        map[string]*meta.Edit
	order        []string
	promptTarget string
	confirm      bool
	preview      []string
	writing      bool
}

type metaWrittenMsg struct {
	path, target string
	err          error
}

func metaRows(fm *meta.FileMeta) []metaRow {
	var rows []metaRow
	groups := make([]string, 0, len(fm.Groups))
	for g := range fm.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		rows = append(rows, metaRow{header: "[" + g + "]"})
		tags := map[string]string{}
		for k, v := range fm.Groups[g] {
			tags[k] = fmt.Sprint(v)
		}
		for _, k := range sortedKeys(tags) {
			rows = append(rows, metaRow{key: g + ":" + k, value: tags[k]}) // group-qualified keys go to exiftool
		}
	}
	if meta.IsImage(fm.Path) {
		return rows
	}
	rows = append(rows, metaRow{header: "CONTAINER"})
	for _, k := range sortedKeys(fm.Tags) {
		rows = append(rows, metaRow{key: k, value: fm.Tags[k]})
	}
	for i := range fm.Tracks {
		t := &fm.Tracks[i]
		rows = append(rows, metaRow{target: t.Selector, track: t, header: fmt.Sprintf("#%d %s %s %s", t.Index, t.Selector, t.Type, t.Codec)})
		for _, k := range sortedKeys(t.Tags) {
			rows = append(rows, metaRow{target: t.Selector, key: k, value: t.Tags[k], track: t})
		}
	}
	return rows
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (e *metaEditor) edit(target string) *meta.Edit {
	if e.edits == nil {
		e.edits = map[string]*meta.Edit{}
	}
	if e.edits[target] == nil {
		e.edits[target] = &meta.Edit{Track: target}
		e.order = append(e.order, target)
	}
	return e.edits[target]
}

func (e *metaEditor) set(target, k, v string) {
	ed := e.edit(target)
	ed.Remove = without(ed.Remove, k)
	for i := range ed.Set {
		if ed.Set[i].Key == k {
			ed.Set[i].Value = v
			return
		}
	}
	ed.Set = append(ed.Set, meta.Tag{Key: k, Value: v})
}

func (e *metaEditor) remove(target, k string) {
	ed := e.edit(target)
	for i := range ed.Set {
		if ed.Set[i].Key == k {
			ed.Set = append(ed.Set[:i], ed.Set[i+1:]...)
			break
		}
	}
	if len(without(ed.Remove, k)) == len(ed.Remove) {
		ed.Remove = append(ed.Remove, k)
	}
}

func without(list []string, s string) []string {
	var out []string
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// pending returns the new value of a tag: set, removed, or untouched (ok == false).
func (e metaEditor) pending(target, k string) (value string, removed, ok bool) {
	ed := e.edits[target]
	if ed == nil {
		return "", false, false
	}
	for _, t := range ed.Set {
		if t.Key == k {
			return t.Value, false, true
		}
	}
	for _, r := range ed.Remove {
		if r == k {
			return "", true, true
		}
	}
	return "", false, false
}

// changes lists the pending edits as diff lines, skipping targets with nothing left.
func (e metaEditor) changes() ([]string, []meta.Edit) {
	var lines []string
	var edits []meta.Edit
	for _, target := range e.order {
		ed := e.edits[target]
		if len(ed.Set)+len(ed.Remove) == 0 {
			continue
		}
		name := "container"
		if target != "" {
			name = "track " + target
		}
		lines = append(lines, headStyle.Render(name))
		for _, t := range ed.Set {
			lines = append(lines, okStyle.Render("+ "+t.Key+" = "+t.Value))
		}
		for _, k := range ed.Remove {
			lines = append(lines, errStyle.Render("- "+k))
		}
		edits = append(edits, *ed)
	}
	return lines, edits
}

func (m model) handleMetaKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	st := m.currentState()
	if st == nil || st.meta == nil || m.meta.writing {
		return m, nil
	}
	rows := metaRows(st.meta)
	if len(rows) == 0 {
		return m, nil
	}
	e := &m.meta
	e.cursor = min(e.cursor, len(rows)-1)
	row := rows[e.cursor]
	switch {
	case key.Matches(msg, keyUp):
		e.cursor = max(e.cursor-1, 0)
	case key.Matches(msg, keyDown):
		e.cursor = min(e.cursor+1, len(rows)-1)
	case key.Matches(msg, keyEditTag) && row.key != "":
		e.promptTarget = row.target
		value := row.value
		if v, removed, ok := e.pending(row.target, row.key); ok && !removed {
			value = v
		}
		return m, m.startPrompt(promptTagValue, row.key, row.key, value)
	case key.Matches(msg, keyAddTag), key.Matches(msg, keyEditTag):
		e.promptTarget = row.target
		return m, m.startPrompt(promptTagAdd, "", "KEY=VALUE", "")
	case key.Matches(msg, keyDelTag) && row.key != "":
		e.remove(row.target, row.key)
	case (key.Matches(msg, keyDefault) || key.Matches(msg, keyForced)) && row.track != nil:
		flag, current := "default", row.track.Default
		if key.Matches(msg, keyForced) {
			flag, current = "forced", row.track.Forced
		}
		if v, _, ok := e.pending(row.target, flag); ok {
			current = v == "1"
		}
		e.set(row.target, flag, map[bool]string{true: "0", false: "1"}[current])
	case key.Matches(msg, keyReset):
		m.meta = metaEditor{cursor: e.cursor}
	case key.Matches(msg, keyWrite):
		path, _ := m.browser.current()
		_, edits := e.changes()
		if len(edits) == 0 {
			m.setStatus(nil, "nothing to write")
			return m, nil
		}
		e.preview = nil
		for _, ed := range edits {
			_, cmd, err := meta.Command(m.ctx, path, "", ed)
			if err != nil {
				e.preview = append(e.preview, errStyle.Render(err.Error()))
				continue
			}
			e.preview = append(e.preview, dimStyle.Render(strings.Join(cmd, " ")))
		}
		e.confirm = true
	}
	return m, nil
}

func (m *model) addTagEdit(kind promptKind, value string) {
	k := m.promptKey
	if kind == promptTagAdd {
		var ok bool
		if k, value, ok = strings.Cut(value, "="); !ok || strings.TrimSpace(k) == "" {
			m.setStatus(fmt.Errorf("expected KEY=VALUE"), "")
			return
		}
		k = strings.TrimSpace(k)
	}
	m.meta.set(m.meta.promptTarget, k, value)
}

func (m model) handleConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	path, _ := m.browser.current()
	_, edits := m.meta.changes()
	switch msg.String() {
	case "y":
		m.meta.confirm, m.meta.writing = false, true
		m.setStatus(nil, "writing…")
		return m, writeMeta(m.ctx, path, "", edits, m.et)
	case "c":
		ext := filepath.Ext(path)
		target := strings.TrimSuffix(path, ext) + ".edited" + ext
		m.meta.confirm, m.meta.writing = false, true
		m.setStatus(nil, "writing %s…", filepath.Base(target))
		return m, writeMeta(m.ctx, path, target, edits, m.et)
	case "n", "esc":
		m.meta.confirm = false
	}
	return m, nil
}

// writeMeta applies the edits one by one. With a copy target the first edit
// creates the copy and the rest change the copy in place.
func writeMeta(ctx context.Context, path, copyTo string, edits []meta.Edit, et *meta.ExifTool) tea.Cmd {
	return func() tea.Msg {
		target := path
		if copyTo != "" {
			target = copyTo
			if _, err := os.Stat(copyTo); err == nil {
				return metaWrittenMsg{path, target, fmt.Errorf("%s %w", copyTo, fsutil.ErrExists)}
			}
		}
		for i, ed := range edits {
			in, out := target, ""
			if copyTo != "" && i == 0 {
				in, out = path, copyTo
			}
			if _, err := meta.Apply(ctx, in, out, ed, false, et); err != nil {
				return metaWrittenMsg{path, target, err}
			}
		}
		return metaWrittenMsg{path, target, nil}
	}
}

func (m model) metaWritten(msg metaWrittenMsg) (tea.Model, tea.Cmd) {
	m.meta.writing = false
	if msg.err != nil {
		m.setStatus(msg.err, "")
		return m, nil
	}
	delete(m.files, msg.path)
	delete(m.files, msg.target)
	m.meta = metaEditor{}
	if msg.target != msg.path {
		if err := m.browser.load(m.browser.dir); err != nil {
			m.setStatus(err, "")
		}
	}
	m.setStatus(nil, "saved %s", filepath.Base(msg.target))
	return m, m.loadCurrent()
}

func (m model) metaLines(st *fileState, w, h int) []string {
	e := m.meta
	diff, _ := e.changes()
	if e.confirm {
		lines := append([]string{boldStyle.Render("Review changes")}, diff...)
		lines = append(lines, "", boldStyle.Render("Commands"))
		lines = append(lines, e.preview...)
		return append(lines, "", headStyle.Render("y")+" write in place   "+headStyle.Render("c")+" write a .edited copy   "+headStyle.Render("esc")+" cancel")
	}

	rows := metaRows(st.meta)
	var list []string
	keyW := 10
	for _, r := range rows {
		keyW = max(keyW, min(len(r.key), 28))
	}
	for i, r := range rows {
		var line string
		switch {
		case r.key == "" && r.track != nil:
			flags := ""
			for _, f := range []struct {
				name string
				on   bool
			}{{"default", r.track.Default}, {"forced", r.track.Forced}} {
				if v, _, ok := e.pending(r.target, f.name); ok {
					f.on = v == "1"
				}
				if f.on {
					flags += "  " + f.name
				}
			}
			line = headStyle.Render(r.header) + okStyle.Render(flags)
		case r.key == "":
			line = headStyle.Render(r.header)
		default:
			value := r.value
			if v, removed, ok := e.pending(r.target, r.key); ok {
				value = okStyle.Render(v)
				if removed {
					value = errStyle.Render("(delete)")
				}
			}
			line = "  " + dimStyle.Render(fmt.Sprintf("%-*s", keyW, r.key)) + " " + value
		}
		if i == min(e.cursor, len(rows)-1) && m.panelFocus {
			line = cursorSty.Render(fitLine(line, w))
		}
		list = append(list, line)
	}
	listH := h
	if len(diff) > 0 {
		listH = max(h-len(diff)-2, 3)
	}
	lines := window(list, e.cursor, listH)
	if len(lines) > listH {
		lines = lines[:listH]
	}
	if len(diff) > 0 {
		lines = append(lines, "", boldStyle.Render(fmt.Sprintf("Pending (%d targets) — w to review", len(e.order))))
		lines = append(lines, diff...)
	}
	return lines
}

func (m model) metaKeys() []key.Binding {
	return []key.Binding{keyUp, keyDown, keyEditTag, keyAddTag, keyDelTag, keyDefault, keyForced, keyReset, keyWrite}
}
