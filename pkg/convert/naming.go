package convert

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"

	"github.com/Mimic890/hypr-encode/pkg/probe"
)

// Policies for outputs that already exist.
const (
	ConflictFail      = "fail"
	ConflictOverwrite = "overwrite"
	ConflictIncrement = "increment" // name.1.ext, name.2.ext, …
)

// Conflicts lists every conflict policy.
var Conflicts = []string{ConflictFail, ConflictOverwrite, ConflictIncrement}

// When an output name template applies.
const (
	ApplySameDir = "same-dir" // only when the output directory is the input's directory
	ApplyAlways  = "always"
)

// Naming decides output names for directory outputs and what happens when one exists.
type Naming struct {
	Template   string `yaml:"template"`    // dot-separated segments with {placeholders}; "" keeps the input name
	Apply      string `yaml:"apply"`       // ApplySameDir (default) or ApplyAlways
	OnConflict string `yaml:"on_conflict"` // ConflictFail (default), ConflictOverwrite or ConflictIncrement
}

// Config is config.yml.
type Config struct {
	Jobs   map[string]int `yaml:"jobs"` // concurrency limit per load class
	Output Naming         `yaml:"output"`
}

// DefaultNaming keeps outputs next to their input from replacing it.
func DefaultNaming() Naming {
	return Naming{Template: "{name}.conv", Apply: ApplySameDir, OnConflict: ConflictFail}
}

// LoadConfig reads config.yml, filling unset values with DefaultLimits and
// DefaultNaming. A missing file is not an error.
func LoadConfig(path string) (Config, error) {
	cfg := Config{Jobs: DefaultLimits(), Output: DefaultNaming()}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	var file Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	for class, n := range file.Jobs {
		if _, ok := cfg.Jobs[class]; !ok || n < 1 {
			return Config{}, fmt.Errorf("%s: jobs.%s: want a load class (%s) with a value of at least 1", path, class, strings.Join(Loads, ", "))
		}
		cfg.Jobs[class] = n
	}
	for _, v := range []struct {
		dst *string
		src string
	}{
		{&cfg.Output.Template, file.Output.Template},
		{&cfg.Output.Apply, file.Output.Apply},
		{&cfg.Output.OnConflict, file.Output.OnConflict},
	} {
		if v.src != "" {
			*v.dst = v.src
		}
	}
	if err := cfg.Output.validate(); err != nil {
		return Config{}, fmt.Errorf("%s: output: %w", path, err)
	}
	return cfg, nil
}

func (n Naming) validate() error {
	switch {
	case !contains([]string{"", ApplySameDir, ApplyAlways}, n.Apply):
		return fmt.Errorf("apply %q must be %s or %s", n.Apply, ApplySameDir, ApplyAlways)
	case !contains(append([]string{""}, Conflicts...), n.OnConflict):
		return fmt.Errorf("on_conflict %q must be one of %s", n.OnConflict, strings.Join(Conflicts, ", "))
	case n.Template != "":
		return checkTemplate(n.Template)
	}
	return nil
}

var (
	placeholder  = regexp.MustCompile(`\{([a-z]+)\}`)
	placeholders = []string{"name", "preset", "vcodec", "encoder", "acodec", "res", "bitdepth", "fps", "crf", "date"}
	probed       = []string{"vcodec", "acodec", "res", "bitdepth", "fps"} // need the input's streams
)

func checkTemplate(t string) error {
	for _, m := range placeholder.FindAllStringSubmatch(t, -1) {
		if !contains(placeholders, m[1]) {
			return fmt.Errorf("name template %q: unknown placeholder %s; use {%s}", t, m[0], strings.Join(placeholders, "}, {"))
		}
	}
	if rest := placeholder.ReplaceAllString(t, ""); strings.ContainsAny(rest, `{}/\`) {
		return fmt.Errorf("name template %q: stray brace or path separator", t)
	}
	return nil
}

// expandTemplate builds an output name (without extension) for input. Each
// dot-separated segment whose placeholders include an empty value is dropped,
// so ".{fps}fps" disappears for audio.
func expandTemplate(tmpl, input string, p Preset, now time.Time) (string, error) {
	vals, err := templateValues(tmpl, input, p, now)
	if err != nil {
		return "", err
	}
	var kept []string
	for _, seg := range strings.Split(tmpl, ".") {
		empty := false
		seg = placeholder.ReplaceAllStringFunc(seg, func(m string) string {
			v := vals[m[1:len(m)-1]]
			empty = empty || v == ""
			return v
		})
		if !empty && seg != "" {
			kept = append(kept, seg)
		}
	}
	name := strings.Join(kept, ".")
	if name == "" || name == ".." {
		return "", fmt.Errorf("name template %q gives an empty name for %s", tmpl, input)
	}
	return name, nil
}

// templateValues returns the placeholder values for one input. Encoded streams
// use the preset's settings; copied streams use what ffprobe reports.
func templateValues(tmpl, input string, p Preset, now time.Time) (map[string]string, error) {
	base := filepath.Base(input)
	main := p.Video
	if p.Media == "audio" {
		main = p.Audio
	}
	vals := map[string]string{
		"name":    strings.TrimSuffix(base, filepath.Ext(base)),
		"preset":  p.Name,
		"encoder": "copy",
		"date":    now.Format("2006-01-02"),
	}
	if !isCopy(main) {
		vals["encoder"] = strings.TrimPrefix(main.Codec, "lib")
		if main.CRF != nil {
			vals["crf"] = strconv.Itoa(*main.CRF)
		}
	}
	if slices.ContainsFunc(probed, func(k string) bool { return strings.Contains(tmpl, "{"+k+"}") }) {
		info, err := probe.ScanFile(input)
		if err != nil {
			return nil, fmt.Errorf("name template needs the streams of %s: %w", input, err)
		}
		// shortcut: values come from the first video and audio stream and ignore raw -vf/-r args; read the selected tracks if presets start scaling or picking streams.
		var video []int
		for _, v := range info.Video {
			video = append(video, v.Index)
		}
		for _, v := range info.Video {
			if v.IsAttachedPic || !keeps(p.Tracks, "v", video) {
				continue
			}
			vals["vcodec"] = v.Codec
			depth := v.BitDepth
			if !isCopy(p.Video) {
				vals["vcodec"] = codecName(p.Video.Codec)
				if d := probe.BitDepthFromPixFmt(p.Video.PixFmt); d > 0 {
					depth = d
				}
			}
			if v.Height > 0 {
				vals["res"] = fmt.Sprintf("%dp", v.Height)
			}
			if depth > 0 {
				vals["bitdepth"] = strconv.Itoa(depth)
			}
			if v.FPS > 0 && p.Media != "image" {
				vals["fps"] = strconv.FormatFloat(math.Round(v.FPS*1000)/1000, 'f', -1, 64)
			}
			break
		}
		var audio []int
		for _, a := range info.Audio {
			audio = append(audio, a.Index)
		}
		if len(info.Audio) > 0 && keeps(p.Tracks, "a", audio) {
			vals["acodec"] = info.Audio[0].Codec
			if !isCopy(p.Audio) {
				vals["acodec"] = codecName(p.Audio.Codec)
			}
		}
	}
	for k, v := range vals {
		vals[k] = strings.Map(func(r rune) rune {
			if r == '/' || r == '\\' || unicode.IsControl(r) {
				return -1
			}
			return r
		}, v)
	}
	return vals, nil
}

// keeps reports whether the track selectors keep any stream of type typ
// ("v" or "a"); indexes are the input's stream indexes of that type.
func keeps(tracks []string, typ string, indexes []int) bool {
	for _, sel := range tracks {
		t, _, _ := strings.Cut(sel, ":")
		if n, err := strconv.Atoi(sel); sel == "all" || t == typ || err == nil && slices.Contains(indexes, n) {
			return true
		}
	}
	return len(tracks) == 0
}

// codecName maps an ffmpeg encoder to the codec name ffprobe reports for its output.
func codecName(encoder string) string {
	switch encoder {
	case "libx264":
		return "h264"
	case "libx265":
		return "hevc"
	case "libsvtav1", "libaom-av1", "librav1e":
		return "av1"
	case "libvpx-vp9":
		return "vp9"
	case "libvpx":
		return "vp8"
	case "libmp3lame":
		return "mp3"
	case "libfdk_aac":
		return "aac"
	}
	for _, hw := range []string{"_nvenc", "_qsv", "_amf", "_vaapi", "_videotoolbox"} {
		if c, ok := strings.CutSuffix(encoder, hw); ok {
			return c
		}
	}
	return strings.TrimPrefix(encoder, "lib") // libopus, libvorbis, libwebp
}

// samePath reports whether a and b name the same file or directory.
func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil && absA == absB {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// numbered returns path with ".n" before its extension.
func numbered(path string, n int) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + "." + strconv.Itoa(n) + ext
}
