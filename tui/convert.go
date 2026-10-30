package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"

	"github.com/Mimic890/hypr-encode/pkg/convert"
)

var (
	keyPresetCycle = key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("←/→", "preset"))
	keyEditField   = key.NewBinding(key.WithKeys("enter", "e"), key.WithHelp("enter", "edit"))
	keyRun         = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "run"))
	keySavePreset  = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "save preset"))
	keyJobs        = key.NewBinding(key.WithKeys("+", "-", "0"), key.WithHelp("+/-/0", "jobs"))
	keyStop        = key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "stop"))
)

// field is one editable override on top of the selected preset. An empty
// value keeps the preset's own.
type field struct {
	label string
	get   func(p convert.Preset) string
	set   func(p *convert.Preset, v string) error
}

var fields = []field{
	{"Video codec", func(p convert.Preset) string { return p.Video.Codec }, func(p *convert.Preset, v string) error { p.Video.Codec = v; return nil }},
	{"Audio codec", func(p convert.Preset) string { return p.Audio.Codec }, func(p *convert.Preset, v string) error { p.Audio.Codec = v; return nil }},
	{"CRF", func(p convert.Preset) string {
		if c := mainCodec(&p); c.CRF != nil {
			return strconv.Itoa(*c.CRF)
		}
		return ""
	}, func(p *convert.Preset, v string) error {
		crf, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CRF: %q is not a number", v)
		}
		mainCodec(p).CRF = &crf
		return nil
	}},
	{"Video bitrate", func(p convert.Preset) string { return p.Video.Bitrate }, func(p *convert.Preset, v string) error { p.Video.Bitrate = v; return nil }},
	{"Audio bitrate", func(p convert.Preset) string { return p.Audio.Bitrate }, func(p *convert.Preset, v string) error { p.Audio.Bitrate = v; return nil }},
	{"Encoder preset", func(p convert.Preset) string { return p.Video.Preset }, func(p *convert.Preset, v string) error { p.Video.Preset = v; return nil }},
	{"Container", func(p convert.Preset) string { return p.Container }, func(p *convert.Preset, v string) error {
		p.Container = strings.TrimPrefix(v, ".")
		return nil
	}},
	{"Tracks", func(p convert.Preset) string { return strings.Join(p.Tracks, ",") }, func(p *convert.Preset, v string) error {
		p.Tracks = strings.Split(strings.ReplaceAll(v, " ", ""), ",")
		return nil
	}},
}

// mainCodec is where CRF goes, matching the CLI: audio for audio presets.
func mainCodec(p *convert.Preset) *convert.Codec {
	if p.Media == "audio" {
		return &p.Audio
	}
	return &p.Video
}

type jobView struct {
	name    string
	state   string // queued, start, progress, done, failed, canceled
	percent float64
	err     string
}

// convertPanel is shared by pointer between model copies; it owns the queue.
type convertPanel struct {
	lib       *convert.Library
	presets   []convert.Preset
	presetIdx int
	loadErr   error
	values    []string // overrides, one per field
	outDir    string
	cursor    int // 0 = preset row, 1..len(fields) = fields, len(fields)+1 = output dir

	queue   *convert.Queue
	cancelF context.CancelFunc
	running bool
	active  int
	msgs    chan tea.Msg
	jobs    []*jobView
	bar     progress.Model
}

type presetsMsg struct {
	lib     *convert.Library
	presets []convert.Preset
	err     error
}

type jobEventMsg convert.Event

type queueDoneMsg struct{}

func newConvertPanel(dir string) *convertPanel {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return &convertPanel{
		values: make([]string, len(fields)),
		outDir: filepath.Join(dir, "converted"),
		bar:    progress.New(progress.WithoutPercentage(), progress.WithDefaultBlend()),
	}
}

// loadPresets reads the library and keeps the presets usable on this machine.
func (c *convertPanel) loadPresets(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		dir, err := convert.ConfigDir()
		if err != nil {
			return presetsMsg{err: err}
		}
		lib, err := convert.LoadLibrary(filepath.Join(dir, "presets.yml"))
		if err != nil {
			return presetsMsg{err: err}
		}
		presets, _, err := lib.Available(ctx)
		return presetsMsg{lib, presets, err}
	}
}

func (c *convertPanel) cancel() {
	if c.cancelF != nil {
		c.cancelF()
	}
}

func (c *convertPanel) next() tea.Cmd {
	ch := c.msgs
	return func() tea.Msg { return <-ch }
}

func (c *convertPanel) update(msg tea.Msg, m *model) tea.Cmd {
	switch msg := msg.(type) {
	case presetsMsg:
		c.lib, c.loadErr = msg.lib, msg.err
		name := ""
		if len(c.presets) > 0 {
			name = c.presets[c.presetIdx].Name
		}
		c.presets, c.presetIdx = msg.presets, 0
		for i, p := range c.presets {
			if p.Name == name {
				c.presetIdx = i
			}
		}
	case jobEventMsg:
		j := c.jobs[msg.JobID-1]
		switch msg.Type {
		case "start":
			c.active++
		case "done", "failed", "canceled":
			if j.state == "start" || j.state == "progress" {
				c.active--
			}
		}
		j.state, j.err = msg.Type, msg.Error
		if msg.Progress != nil {
			j.percent = msg.Progress.Percent / 100
		}
		if msg.Type == "done" {
			j.percent = 1
		}
		return c.next()
	case queueDoneMsg:
		c.running, c.active = false, 0
		c.cancel()
		failed := 0
		for _, j := range c.jobs {
			if j.state != "done" {
				failed++
			}
		}
		if failed > 0 {
			m.setStatus(fmt.Errorf("%d of %d jobs did not finish", failed, len(c.jobs)), "")
		} else {
			m.setStatus(nil, "converted %d files to %s", len(c.jobs), c.outDir)
		}
		if err := m.browser.load(m.browser.dir); err == nil {
			return m.loadCurrent() // outputs may land in the listed directory
		}
	}
	return nil
}

func (c *convertPanel) preset() (convert.Preset, bool) {
	if len(c.presets) == 0 {
		return convert.Preset{}, false
	}
	return c.presets[c.presetIdx], true
}

// build applies the overrides to the selected preset.
func (c *convertPanel) build() (convert.Preset, error) {
	p, ok := c.preset()
	if !ok {
		return p, errors.New("no presets available")
	}
	for i, f := range fields {
		if c.values[i] != "" {
			if err := f.set(&p, c.values[i]); err != nil {
				return p, err
			}
		}
	}
	return p, nil
}

func (c *convertPanel) handleKey(msg tea.KeyPressMsg, m *model) tea.Cmd {
	rows := len(fields) + 2
	switch {
	case key.Matches(msg, keyUp):
		c.cursor = max(c.cursor-1, 0)
	case key.Matches(msg, keyDown):
		c.cursor = min(c.cursor+1, rows-1)
	case key.Matches(msg, keyPresetCycle) && c.cursor == 0 && len(c.presets) > 0:
		step := 1
		if msg.String() == "left" {
			step = len(c.presets) - 1
		}
		c.presetIdx = (c.presetIdx + step) % len(c.presets)
	case key.Matches(msg, keyEditField) && c.cursor > 0:
		i := c.cursor - 1
		if i == len(fields) {
			return m.startPrompt(promptField, strconv.Itoa(i), "Output dir", c.outDir)
		}
		return m.startPrompt(promptField, strconv.Itoa(i), fields[i].label+" (empty = preset)", c.values[i])
	case key.Matches(msg, keyRun):
		return c.run(m)
	case key.Matches(msg, keySavePreset):
		if _, ok := c.preset(); ok {
			return m.startPrompt(promptPresetName, "", "Save as preset", "")
		}
	case key.Matches(msg, keyJobs) && c.queue != nil:
		n := c.queue.Jobs()
		if n == 0 {
			n = max(c.active, 1) // leaving auto mode starts from what runs now
		}
		switch msg.String() {
		case "+":
			n++
		case "-":
			n = max(n-1, 1)
		case "0":
			n = 0
		}
		c.queue.SetJobs(n)
	case key.Matches(msg, keyStop) && c.running:
		c.cancel()
		m.setStatus(nil, "stopping jobs…")
	}
	return nil
}

func (c *convertPanel) submitPrompt(kind promptKind, keyName, value string, m *model) tea.Cmd {
	value = strings.TrimSpace(value)
	if kind == promptField {
		i, _ := strconv.Atoi(keyName)
		if i == len(fields) {
			c.outDir = value
		} else {
			c.values[i] = value
		}
		return nil
	}
	if value == "" {
		return nil
	}
	if existing, err := c.lib.Get(value); err == nil && existing.Source == "user" {
		m.setStatus(fmt.Errorf("user preset %s exists; remove it with: hypr-encode preset rm --yes %s", value, value), "")
		return nil
	}
	base, _ := c.preset()
	p := convert.Preset{Name: value, Extends: base.Name}
	for i, f := range fields {
		if c.values[i] != "" {
			if err := f.set(&p, c.values[i]); err != nil {
				m.setStatus(err, "")
				return nil
			}
		}
	}
	if p.Media == "" && base.Media == "audio" && p.Video.CRF != nil {
		p.Audio.CRF, p.Video.CRF = p.Video.CRF, nil // CRF targets the base's main stream
	}
	if err := c.lib.Save(p); err != nil {
		m.setStatus(err, "")
		return nil
	}
	m.setStatus(nil, "saved preset %s", value)
	return c.loadPresets(m.ctx)
}

func (c *convertPanel) run(m *model) tea.Cmd {
	if c.running {
		m.setStatus(errors.New("a queue is already running"), "")
		return nil
	}
	inputs := m.browser.markedPaths()
	if len(inputs) == 0 {
		if path, isDir := m.browser.current(); !isDir && path != "" {
			inputs = []string{path}
		}
	}
	if len(inputs) == 0 {
		m.setStatus(errors.New("mark files with space or select one"), "")
		return nil
	}
	p, err := c.build()
	if err == nil {
		err = p.Normalize()
	}
	var jobs []convert.Job
	if err == nil {
		jobs, err = convert.Plan(inputs, filepath.Clean(c.outDir)+string(filepath.Separator), p, false)
	}
	var limits map[string]int
	if err == nil {
		var dir string
		if dir, err = convert.ConfigDir(); err == nil {
			limits, err = convert.LoadLimits(filepath.Join(dir, "config.yml"))
		}
	}
	if err != nil {
		m.setStatus(err, "")
		return nil
	}

	ctx, cancel := context.WithCancel(m.ctx)
	c.cancelF, c.running, c.active = cancel, true, 0
	c.msgs = make(chan tea.Msg, 256)
	c.jobs = make([]*jobView, len(jobs))
	for i, j := range jobs {
		c.jobs[i] = &jobView{name: filepath.Base(j.Input), state: "queued"}
	}
	jobsN := 0
	if c.queue != nil {
		jobsN = c.queue.Jobs() // keep a manual job count between runs
	}
	msgs := c.msgs
	c.queue = &convert.Queue{Limits: limits, Cores: runtime.NumCPU(), OnEvent: func(ev convert.Event) { msgs <- jobEventMsg(ev) }}
	c.queue.SetJobs(jobsN)
	q := c.queue
	go func() {
		q.Run(ctx, jobs) // errors arrive as failed events
		msgs <- queueDoneMsg{}
	}()
	m.setStatus(nil, "converting %d files with %s…", len(jobs), p.Name)
	return c.next()
}

func (c *convertPanel) lines(w, h int, m *model) []string {
	var lines []string
	switch {
	case c.loadErr != nil:
		return []string{errStyle.Render(c.loadErr.Error())}
	case c.lib == nil:
		return []string{dimStyle.Render("Checking presets…")}
	}
	p, ok := c.preset()
	row := func(i int, label, value string) {
		line := dimStyle.Render(fmt.Sprintf("%-15s", label)) + " " + value
		if i == c.cursor && m.panelFocus {
			line = cursorSty.Render(fitLine(line, w))
		}
		lines = append(lines, line)
	}
	if ok {
		row(0, "Preset", "◂ "+headStyle.Render(p.Name)+" ▸ "+dimStyle.Render(p.Description))
	} else {
		row(0, "Preset", errStyle.Render("none usable; see hypr-encode doctor"))
	}
	for i, f := range fields {
		value := c.values[i]
		if value == "" {
			value = dimStyle.Render(f.get(p))
		}
		row(i+1, f.label, value)
	}
	row(len(fields)+1, "Output dir", c.outDir)

	inputs := len(m.browser.markedPaths())
	target := fmt.Sprintf("%d marked files", inputs)
	if inputs == 0 {
		target = "the selected file"
	}
	jobs := "auto"
	if c.queue != nil && c.queue.Jobs() > 0 {
		jobs = strconv.Itoa(c.queue.Jobs())
	}
	lines = append(lines, "", dimStyle.Render(fmt.Sprintf("r converts %s · jobs %s · %d running", target, jobs, c.active)))
	if len(c.jobs) == 0 {
		return lines
	}

	nameW := min(24, w/3)
	barW := max(w-nameW-16, 5)
	c.bar.SetWidth(barW)
	total, done := 0.0, 0
	var jobLines []string
	for _, j := range c.jobs {
		total += j.percent
		state := fmt.Sprintf("%3.0f%%", j.percent*100)
		switch j.state {
		case "done":
			done++
			state = okStyle.Render("done")
		case "failed":
			state = errStyle.Render("failed")
		case "canceled", "queued":
			state = dimStyle.Render(j.state)
		}
		jobLines = append(jobLines, fitLine(j.name, nameW)+" "+c.bar.ViewAs(j.percent)+" "+state)
		if j.err != "" {
			jobLines = append(jobLines, "  "+errStyle.Render(j.err))
		}
	}
	lines = append(lines, "", fitLine(boldStyle.Render("Total"), nameW)+" "+c.bar.ViewAs(total/float64(len(c.jobs)))+fmt.Sprintf(" %d/%d", done, len(c.jobs)))
	room := max(h-len(lines), 1) // shortcut: long queues show only their first jobs; scroll if queues of dozens become common
	return append(lines, jobLines[:min(len(jobLines), room)]...)
}

func (c *convertPanel) keys() []key.Binding {
	return []key.Binding{keyUp, keyDown, keyPresetCycle, keyEditField, keyRun, keySavePreset, keyJobs, keyStop}
}
