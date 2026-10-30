package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Mimic890/hypr-encode/internal/testmedia"
	"github.com/Mimic890/hypr-encode/pkg/meta"
)

func press(m tea.Model, keys ...string) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "down":
			msg = tea.KeyPressMsg{Code: tea.KeyDown}
		default:
			for _, r := range k {
				m, cmd = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			continue
		}
		m, cmd = m.Update(msg)
	}
	return m, cmd
}

// drain runs cmd and feeds its messages back until nothing is left.
func drain(t *testing.T, m tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	for i := 0; cmd != nil && i < 10000; i++ {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				m = drain(t, m, c)
			}
			return m
		}
		if msg == nil {
			return m
		}
		m, cmd = m.Update(msg)
	}
	return m
}

func newTestModel(t *testing.T, dir string) tea.Model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	m, err := newModel(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.et != nil {
		t.Cleanup(func() { m.et.Close() })
	}
	return drain(t, m, m.Init())
}

func checkView(t *testing.T, m tea.Model, w, h int) {
	t.Helper()
	lines := strings.Split(m.(model).render(), "\n")
	if w >= 50 && h >= 12 && len(lines) != h {
		t.Errorf("%dx%d: %d lines", w, h, len(lines))
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Errorf("%dx%d: line %d is %d wide: %q", w, h, i, ansi.StringWidth(l), ansi.Strip(l))
		}
	}
}

func TestLayoutFitsEverySize(t *testing.T) {
	file := testmedia.Generate(t, "clip.mkv")
	m := newTestModel(t, filepath.Dir(file))
	m, cmd := press(m, "down") // ".." is first
	m = drain(t, m, cmd)
	for _, size := range [][2]int{{30, 8}, {50, 12}, {200, 60}, {80, 24}} {
		m, _ = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, tab := range []string{"1", "2", "3", "4"} {
			m, _ = press(m, tab)
			checkView(t, m, size[0], size[1])
		}
	}
	if !strings.Contains(ansi.Strip(m.(model).render()), "clip.mkv") {
		t.Error("file list does not show clip.mkv")
	}
}

func TestBrowserNavigation(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t, dir)
	m, _ = press(m, "down", "enter")
	if got := m.(model).browser.dir; got != filepath.Join(dir, "sub") {
		t.Fatalf("dir = %s", got)
	}
	m, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	b := m.(model).browser
	if b.dir != dir || b.entries[b.cursor].name != "sub" {
		t.Fatalf("back: dir %s cursor on %s", b.dir, b.entries[b.cursor].name)
	}
}

func TestMetaEditWritesCopy(t *testing.T) {
	file := testmedia.Generate(t, "clip.mkv")
	m := newTestModel(t, filepath.Dir(file))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m, cmd := press(m, "down")
	m = drain(t, m, cmd)
	m, _ = press(m, "tab", "3", "a", "title=Edited", "enter", "w")
	if !m.(model).meta.confirm {
		t.Fatalf("no confirm screen; status %q", m.(model).status)
	}
	if !strings.Contains(ansi.Strip(m.(model).render()), "+ title = Edited") {
		t.Error("preview lacks the pending change")
	}
	m, cmd = press(m, "c")
	m = drain(t, m, cmd)
	if m.(model).statusErr {
		t.Fatal(m.(model).status)
	}
	copyPath := strings.TrimSuffix(file, ".mkv") + ".edited.mkv"
	fm, err := meta.Read(context.Background(), copyPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fm.Tags["title"] != "Edited" {
		t.Errorf("title = %q", fm.Tags["title"])
	}
	if orig, _ := meta.Read(context.Background(), file, nil); orig.Tags["title"] == "Edited" {
		t.Error("source was changed")
	}
}

func TestConvertQueueRuns(t *testing.T) {
	file := testmedia.Generate(t, "clip.mkv")
	dir := filepath.Dir(file)
	m := newTestModel(t, dir)
	m, cmd := press(m, "down", " ") // mark clip.mkv
	m = drain(t, m, cmd)
	m, _ = press(m, "tab", "4")
	c := m.(model).conv
	for i, p := range c.presets {
		if p.Name == "remux" {
			c.presetIdx = i
		}
	}
	m, _ = press(m, "+") // no queue yet: ignored
	m, cmd = press(m, "r")
	m = drain(t, m, cmd)
	if m.(model).statusErr {
		t.Fatal(m.(model).status)
	}
	if c.running || len(c.jobs) != 1 || c.jobs[0].state != "done" {
		t.Fatalf("running %v jobs %+v", c.running, c.jobs[0])
	}
	if _, err := os.Stat(filepath.Join(dir, "converted", "clip.mkv")); err != nil {
		t.Fatal(err)
	}
}
