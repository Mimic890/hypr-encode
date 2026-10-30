// Package convert builds and runs ffmpeg conversions from presets,
// with a parallel job queue and live progress.
package convert

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
)

// Load classes tell the queue how many jobs of a kind may run at once.
const (
	LoadCPUHeavy = "cpu-heavy"
	LoadLight    = "light"
	LoadGPU      = "gpu"
	LoadIO       = "io"
)

// Loads lists every load class.
var Loads = []string{LoadCPUHeavy, LoadLight, LoadGPU, LoadIO}

// Codec configures the encoder for one stream type.
type Codec struct {
	Codec   string   `yaml:"codec,omitempty" json:"codec,omitempty"`     // ffmpeg encoder name or "copy"
	CRF     *int     `yaml:"crf,omitempty" json:"crf,omitempty"`         // constant quality, mapped per encoder
	Bitrate string   `yaml:"bitrate,omitempty" json:"bitrate,omitempty"` // e.g. "192k"
	Preset  string   `yaml:"preset,omitempty" json:"preset,omitempty"`   // encoder speed preset
	PixFmt  string   `yaml:"pixFmt,omitempty" json:"pixFmt,omitempty"`
	Args    []string `yaml:"args,omitempty" json:"args,omitempty"` // raw encoder args
}

// Preset is a named set of conversion settings.
type Preset struct {
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Extends     string   `yaml:"extends,omitempty" json:"extends,omitempty"`
	Media       string   `yaml:"media,omitempty" json:"media,omitempty"`         // video, audio or image
	Container   string   `yaml:"container,omitempty" json:"container,omitempty"` // output extension for directory output
	Load        string   `yaml:"load,omitempty" json:"load,omitempty"`
	Video       Codec    `yaml:"video,omitempty" json:"video"`
	Audio       Codec    `yaml:"audio,omitempty" json:"audio"`
	Subtitle    Codec    `yaml:"subtitle,omitempty" json:"subtitle"`
	Tracks      []string `yaml:"tracks,omitempty" json:"tracks,omitempty"`       // track selectors; empty keeps all
	InputArgs   []string `yaml:"inputArgs,omitempty" json:"inputArgs,omitempty"` // raw args before -i
	Args        []string `yaml:"args,omitempty" json:"args,omitempty"`           // raw output args
	Source      string   `yaml:"-" json:"source"`                                // "builtin" or "user"
}

// ErrUnknownPreset is returned for preset names that are not defined.
var ErrUnknownPreset = errors.New("unknown preset")

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// Normalize fills the default load class and checks every field.
func (p *Preset) Normalize() error {
	if p.Load == "" {
		p.Load = LoadCPUHeavy
		if isCopy(p.Video) && isCopy(p.Audio) && isCopy(p.Subtitle) {
			p.Load = LoadIO
		}
	}
	switch {
	case !namePattern.MatchString(p.Name):
		return fmt.Errorf("preset name %q: use lowercase letters, digits, '.', '_' or '-'", p.Name)
	case !contains([]string{"", "video", "audio", "image"}, p.Media):
		return fmt.Errorf("preset %s: media %q must be video, audio or image", p.Name, p.Media)
	case !contains(Loads, p.Load):
		return fmt.Errorf("preset %s: load %q must be one of %s", p.Name, p.Load, strings.Join(Loads, ", "))
	case strings.ContainsAny(p.Container, `/\.`):
		return fmt.Errorf("preset %s: container %q must be a bare extension like mkv", p.Name, p.Container)
	}
	for _, sel := range p.Tracks {
		if _, err := mapArg(sel); err != nil {
			return fmt.Errorf("preset %s: %w", p.Name, err)
		}
	}
	return nil
}

func isCopy(c Codec) bool { return c.Codec == "" || c.Codec == "copy" }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

//go:embed builtin.yml
var builtinYAML []byte

// Library holds the built-in presets and the user's presets file.
type Library struct {
	path    string     // user presets file; "" for built-ins only
	doc     *yaml.Node // user file document, kept to preserve comments on save
	builtin map[string]*yaml.Node
	user    map[string]*yaml.Node
	order   []string
}

// LoadLibrary reads the built-in presets and, if it exists, the user file at path.
func LoadLibrary(path string) (*Library, error) {
	l := &Library{path: path, builtin: map[string]*yaml.Node{}, user: map[string]*yaml.Node{}}
	doc, err := parsePresets(builtinYAML, l.builtin)
	if err != nil {
		return nil, fmt.Errorf("built-in presets: %w", err)
	}
	l.order = presetNames(doc)
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if l.doc, err = parsePresets(data, l.user); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	if err := l.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return l, nil
}

// parsePresets validates a presets file strictly and indexes its entries by name.
func parsePresets(data []byte, into map[string]*yaml.Node) (*yaml.Node, error) {
	var strict []Preset
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&strict); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	doc := &yaml.Node{}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode}}}
	}
	for i, node := range doc.Content[0].Content {
		name := strict[i].Name
		if into[name] != nil {
			return nil, fmt.Errorf("preset %q is defined twice", name)
		}
		into[name] = node
	}
	return doc, nil
}

func presetNames(doc *yaml.Node) []string {
	var names []string
	for _, node := range doc.Content[0].Content {
		var head struct{ Name string }
		node.Decode(&head)
		names = append(names, head.Name)
	}
	return names
}

// check resolves every preset so broken files fail at load time.
func (l *Library) check() error {
	for _, name := range l.Names() {
		if _, err := l.Get(name); err != nil {
			return err
		}
	}
	return nil
}

// Names lists built-in presets first, then user-only presets, in file order.
func (l *Library) Names() []string {
	names := append([]string(nil), l.order...)
	if l.doc != nil {
		for _, name := range presetNames(l.doc) {
			if l.builtin[name] == nil {
				names = append(names, name)
			}
		}
	}
	return names
}

// Get returns a preset with its extends chain resolved. User presets win over built-ins.
func (l *Library) Get(name string) (Preset, error) { return l.resolve(name, false, 0) }

// IsBuiltin reports whether name is a built-in preset (overridden or not).
func (l *Library) IsBuiltin(name string) bool { return l.builtin[name] != nil }

func (l *Library) resolve(name string, builtinOnly bool, depth int) (Preset, error) {
	if depth > 16 {
		return Preset{}, fmt.Errorf("preset %s: extends chain is cyclic or too deep", name)
	}
	node, source := l.user[name], "user"
	if node == nil || builtinOnly {
		node, source = l.builtin[name], "builtin"
	}
	if node == nil {
		return Preset{}, fmt.Errorf("%w %q", ErrUnknownPreset, name)
	}
	var head struct{ Extends string }
	if err := node.Decode(&head); err != nil {
		return Preset{}, err
	}
	var p Preset
	if head.Extends != "" {
		// Built-ins only extend built-ins; a user preset may extend the built-in it overrides.
		base, err := l.resolve(head.Extends, source == "builtin" || head.Extends == name, depth+1)
		if err != nil {
			return Preset{}, fmt.Errorf("preset %s: %w", name, err)
		}
		p = base
	}
	// Decoding over the base overwrites only the fields this entry sets.
	if err := node.Decode(&p); err != nil {
		return Preset{}, fmt.Errorf("preset %s: %w", name, err)
	}
	p.Name, p.Extends, p.Source = name, head.Extends, source
	return p, p.Normalize()
}

// Save adds p to the user file, replacing a user preset with the same name.
// p is stored as given, so Extends and unset fields stay unresolved.
func (l *Library) Save(p Preset) error {
	if l.path == "" {
		return errors.New("no user presets file")
	}
	p.Source = ""
	node := &yaml.Node{}
	if err := node.Encode(p); err != nil {
		return err
	}
	seq := l.doc.Content[0]
	if old := l.user[p.Name]; old != nil {
		*old = *node
		node = old
	} else {
		seq.Content = append(seq.Content, node)
	}
	l.user[p.Name] = node
	if _, err := l.Get(p.Name); err != nil {
		return err
	}
	return l.write()
}

// Remove deletes a user preset. Built-in presets cannot be removed.
func (l *Library) Remove(name string) error {
	node := l.user[name]
	if node == nil {
		if l.builtin[name] != nil {
			return fmt.Errorf("preset %s is built-in and read-only", name)
		}
		return fmt.Errorf("%w %q", ErrUnknownPreset, name)
	}
	seq := l.doc.Content[0]
	for i, n := range seq.Content {
		if n == node {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			break
		}
	}
	delete(l.user, name)
	if err := l.check(); err != nil {
		return fmt.Errorf("cannot remove %s: %w", name, err)
	}
	return l.write()
}

func (l *Library) write() error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(l.doc); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	return fsutil.WriteFile(l.path, buf.Bytes())
}

// ConfigDir is where presets.yml and config.yml live.
func ConfigDir() (string, error) {
	dir, err := os.UserConfigDir()
	return filepath.Join(dir, "hypr-encode"), err
}
