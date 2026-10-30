// Package meta reads and writes container, track and image metadata using
// ffprobe/ffmpeg, mkvpropedit and exiftool.
package meta

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// Backends that write metadata.
const (
	BackendMkvpropedit = "mkvpropedit"
	BackendExifTool    = "exiftool"
	BackendFFmpeg      = "ffmpeg"
)

// Track is the metadata of one stream.
type Track struct {
	Index    int               `json:"index"`
	Selector string            `json:"selector"` // TYPE:N, as accepted by Edit.Track
	Type     string            `json:"type"`     // video, audio or subtitle
	Codec    string            `json:"codec"`
	Default  bool              `json:"default"`
	Forced   bool              `json:"forced"`
	Tags     map[string]string `json:"tags"`
}

// FileMeta is the metadata of one file.
type FileMeta struct {
	Path   string                    `json:"path"`
	Tags   map[string]string         `json:"tags"` // container tags from ffprobe
	Tracks []Track                   `json:"tracks"`
	Groups map[string]map[string]any `json:"groups,omitempty"` // exiftool tags by family 1 group
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".tif": true, ".tiff": true,
	".heic": true, ".heif": true, ".avif": true, ".gif": true,
}

// quickTimeExts are video containers exiftool can also write.
var quickTimeExts = map[string]bool{".mp4": true, ".m4v": true, ".mov": true, ".3gp": true}

var matroskaExts = map[string]bool{".mkv": true, ".mka": true, ".mks": true, ".mk3d": true, ".webm": true}

func ext(path string) string { return strings.ToLower(filepath.Ext(path)) }

// IsImage reports whether path has a still-image extension; images are handled by exiftool.
func IsImage(path string) bool { return imageExts[ext(path)] || probe.IsRaw(path) }

// ReadsExif reports whether Read lists exiftool tag groups for path.
func ReadsExif(path string) bool { return IsImage(path) || quickTimeExts[ext(path)] }

// Read collects container and track tags with ffprobe. For images, MP4 and MOV
// it also reads every exiftool tag group when et is not nil; for images
// ffprobe cannot decode (camera RAW) the exiftool groups alone are returned.
func Read(ctx context.Context, path string, et *ExifTool) (*FileMeta, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fm := &FileMeta{Path: abs, Tags: map[string]string{}, Tracks: []Track{}}
	pctx, cancel := context.WithTimeout(ctx, probe.DefaultTimeout)
	info, err := probe.ScanFileContext(pctx, abs)
	cancel()
	if err == nil {
		fm.Tags, fm.Tracks = info.Tags, tracksOf(info)
	}
	if et != nil && ReadsExif(abs) {
		if fm.Groups, err = et.ReadGroups(abs); err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	return fm, nil
}

func tracksOf(info *probe.MediaInfo) []Track {
	var tracks []Track
	for i, v := range info.Video {
		tracks = append(tracks, Track{v.Index, fmt.Sprintf("v:%d", i), "video", v.Codec, v.Default, false, v.Tags})
	}
	for i, a := range info.Audio {
		tracks = append(tracks, Track{a.Index, fmt.Sprintf("a:%d", i), "audio", a.Codec, a.Default, false, a.Tags})
	}
	for i, s := range info.Subtitle {
		tracks = append(tracks, Track{s.Index, fmt.Sprintf("s:%d", i), "subtitle", s.Codec, s.Default, s.Forced, s.Tags})
	}
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].Index < tracks[j].Index })
	return tracks
}

// Tag is one key=value pair to write.
type Tag struct{ Key, Value string }

// Edit is one metadata change on a file. The keys "default" and "forced"
// set track dispositions and take 1/0, yes/no or true/false.
type Edit struct {
	Track        string   // "" edits the container; otherwise TYPE:N (v, a, s) or a stream index
	Set          []Tag    // tags to write
	Remove       []string // tags to delete
	RemoveGroups []string // exiftool groups to clear, e.g. GPS or XMP
	CopyFrom     string   // copy all tags from this file first (exiftool)
}

var (
	keyPattern   = regexp.MustCompile(`^([A-Za-z0-9-]+:)?[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
	groupPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)
	trackPattern = regexp.MustCompile(`^([vas]:)?[0-9]+$`)
)

func isFlag(key string) bool {
	return strings.EqualFold(key, "default") || strings.EqualFold(key, "forced")
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "1", "yes", "true":
		return true, nil
	case "0", "no", "false":
		return false, nil
	}
	return false, fmt.Errorf("%q is not a boolean; use 1/0, yes/no or true/false", s)
}

// exifOnly reports whether the edit uses features only exiftool provides.
func (e Edit) exifOnly() bool {
	if e.CopyFrom != "" || len(e.RemoveGroups) > 0 {
		return true
	}
	for _, t := range e.Set {
		if strings.Contains(t.Key, ":") {
			return true
		}
	}
	for _, k := range e.Remove {
		if strings.Contains(k, ":") {
			return true
		}
	}
	return false
}

// Validate checks tag names, flag values and the track selector.
func (e Edit) Validate() error {
	if e.Track != "" && !trackPattern.MatchString(e.Track) {
		return fmt.Errorf("track %q: use v:N, a:N, s:N or a stream index", e.Track)
	}
	if len(e.Set)+len(e.Remove)+len(e.RemoveGroups) == 0 && e.CopyFrom == "" {
		return fmt.Errorf("nothing to change")
	}
	for _, t := range e.Set {
		if !keyPattern.MatchString(t.Key) {
			return fmt.Errorf("invalid tag name %q", t.Key)
		}
		if isFlag(t.Key) {
			if e.Track == "" {
				return fmt.Errorf("%s is a track flag; choose a track", t.Key)
			}
			if _, err := parseBool(t.Value); err != nil {
				return fmt.Errorf("%s: %w", t.Key, err)
			}
		}
	}
	for _, k := range e.Remove {
		if !keyPattern.MatchString(k) {
			return fmt.Errorf("invalid tag name %q", k)
		}
	}
	for _, g := range e.RemoveGroups {
		if !groupPattern.MatchString(g) {
			return fmt.Errorf("invalid group name %q", g)
		}
	}
	return nil
}

// Backend picks the tool for an edit: exiftool for images and for
// exiftool-only edits on MP4/MOV; mkvpropedit for in-place Matroska edits of
// properties it supports; an ffmpeg remux with -c copy for everything else.
func Backend(path string, e Edit, inPlace bool) (string, error) {
	switch {
	case IsImage(path):
		if e.Track != "" {
			return "", fmt.Errorf("%s: images have no tracks", filepath.Base(path))
		}
		return BackendExifTool, nil
	case e.exifOnly():
		if !quickTimeExts[ext(path)] || e.Track != "" {
			return "", fmt.Errorf("%s: group tags, group removal and copying tags need exiftool, which handles images and MP4/MOV containers only", filepath.Base(path))
		}
		return BackendExifTool, nil
	case matroskaExts[ext(path)] && inPlace:
		if _, ok := mkvpropeditArgs(path, e); ok {
			return BackendMkvpropedit, nil
		}
	}
	return BackendFFmpeg, nil
}

// Command returns the backend and full command line Apply would run for in → out
// (out == "" means in place), with the final path in place of the temp file.
func Command(ctx context.Context, in, out string, e Edit) (string, []string, error) {
	inPlace := out == ""
	if inPlace {
		out = in
	}
	in, out, e, err := prepare(ctx, in, out, e)
	if err != nil {
		return "", nil, err
	}
	backend, err := Backend(in, e, inPlace)
	if err != nil {
		return "", nil, err
	}
	args, err := commandArgs(backend, in, out, e)
	return backend, append([]string{backend}, args...), err
}

// prepare validates the edit, makes paths absolute and checks the track exists.
func prepare(ctx context.Context, in, out string, e Edit) (string, string, Edit, error) {
	if err := e.Validate(); err != nil {
		return "", "", e, err
	}
	var err error
	if in, err = filepath.Abs(in); err != nil {
		return "", "", e, err
	}
	if out, err = filepath.Abs(out); err != nil {
		return "", "", e, err
	}
	if e.CopyFrom != "" {
		if e.CopyFrom, err = filepath.Abs(e.CopyFrom); err != nil {
			return "", "", e, err
		}
	}
	if e.Track != "" {
		// ffmpeg silently ignores metadata for a missing stream, so check first.
		pctx, cancel := context.WithTimeout(ctx, probe.DefaultTimeout)
		info, err := probe.ScanFileContext(pctx, in)
		cancel()
		if err != nil {
			return "", "", e, err
		}
		if !hasTrack(tracksOf(info), e.Track) {
			return "", "", e, fmt.Errorf("%s has no track %s", filepath.Base(in), e.Track)
		}
	}
	return in, out, e, nil
}

func hasTrack(tracks []Track, sel string) bool {
	for _, t := range tracks {
		if t.Selector == sel || strconv.Itoa(t.Index) == sel {
			return true
		}
	}
	return false
}

func commandArgs(backend, in, out string, e Edit) ([]string, error) {
	switch backend {
	case BackendMkvpropedit:
		args, _ := mkvpropeditArgs(in, e)
		return args, nil
	case BackendExifTool:
		return exiftoolArgs(in, out, e), nil
	}
	return ffmpegArgs(in, out, e), nil
}

// Apply writes the edit. With out == "" the file is changed in place;
// otherwise in is left untouched and out is created, replacing an existing
// file only with overwrite. ffmpeg and exiftool write a temp file next to the
// target, renamed into place on success; mkvpropedit edits the file itself.
// et may be nil; an exiftool process is then started for this call.
func Apply(ctx context.Context, in, out string, e Edit, overwrite bool, et *ExifTool) (backend string, err error) {
	inPlace := out == ""
	if inPlace {
		out = in
	}
	if in, out, e, err = prepare(ctx, in, out, e); err != nil {
		return "", err
	}
	if backend, err = Backend(in, e, inPlace); err != nil {
		return "", err
	}
	if !inPlace {
		if _, err := os.Stat(out); err == nil && !overwrite {
			return backend, fmt.Errorf("%s %w", out, fsutil.ErrExists)
		}
	}

	if backend == BackendMkvpropedit {
		args, _ := mkvpropeditArgs(in, e)
		return backend, runTool(ctx, BackendMkvpropedit, 5*time.Minute, args)
	}

	tmp, err := fsutil.TempPath(out)
	if err != nil {
		return backend, err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	switch backend {
	case BackendExifTool:
		os.Remove(tmp) // exiftool -o refuses to replace an existing file
		if et == nil {
			if et, err = StartExifTool(ctx); err != nil {
				return backend, err
			}
			defer et.Close()
		}
		_, stderr, err := et.Exec(exiftoolArgs(in, tmp, e)...)
		if err == nil {
			err = writeError(stderr)
		}
		if err != nil {
			return backend, fmt.Errorf("exiftool on %q: %w", filepath.Base(in), err)
		}
	default:
		if err := runTool(ctx, BackendFFmpeg, 0, ffmpegArgs(in, tmp, e)); err != nil {
			return backend, err
		}
	}
	return backend, fsutil.Commit(tmp, out, overwrite || inPlace)
}

// runTool runs mkvpropedit or ffmpeg and turns a failure into its last output lines.
func runTool(ctx context.Context, name string, timeout time.Duration, args []string) error {
	path, err := tools.Path(name)
	if err != nil {
		return err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, path, args...).CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return fmt.Errorf("%s failed: %s", name, strings.Join(lines[max(len(lines)-3, 0):], "; "))
	}
	return nil
}

// ffmpegArgs remuxes in to out with every stream copied and the edit applied.
func ffmpegArgs(in, out string, e Edit) []string {
	args := []string{"-hide_banner", "-nostdin", "-y", "-v", "error", "-i", in, "-map", "0", "-c", "copy"}
	if matroskaExts[ext(out)] {
		args = append(args, "-default_mode", "passthrough") // keep default flags exactly as edited
	}
	metaOpt, disposition := "-metadata", ""
	for _, t := range e.Set {
		if isFlag(t.Key) {
			on, _ := parseBool(t.Value)
			disposition += map[bool]string{true: "+", false: "-"}[on] + strings.ToLower(t.Key)
			continue
		}
		args = append(args, metaOpt+streamSpec(e.Track), t.Key+"="+t.Value)
	}
	for _, k := range e.Remove {
		if isFlag(k) {
			disposition += "-" + strings.ToLower(k)
			continue
		}
		args = append(args, metaOpt+streamSpec(e.Track), k+"=")
	}
	if disposition != "" {
		args = append(args, "-disposition:"+e.Track, disposition)
	}
	return append(args, out)
}

// streamSpec turns a track selector into the -metadata suffix ffmpeg expects.
func streamSpec(track string) string {
	if track == "" {
		return ""
	}
	return ":s:" + track
}

// mkvpropeditArgs edits Matroska in place. ok is false when the edit needs
// something mkvpropedit cannot do (arbitrary tags), which then takes a remux.
func mkvpropeditArgs(path string, e Edit) (args []string, ok bool) {
	if e.Track == "" {
		args = []string{"--edit", "info"}
	} else {
		typ, n, hasType := strings.Cut(e.Track, ":")
		if !hasType {
			typ, n = "", e.Track
		}
		i, _ := strconv.Atoi(n)
		args = []string{"--edit", "track:" + typ + strconv.Itoa(i+1)} // mkvpropedit counts from 1
	}
	props := map[string]string{"title": "title"}
	if e.Track != "" {
		props = map[string]string{"title": "name", "language": "language", "default": "flag-default", "forced": "flag-forced"}
	}
	for _, t := range e.Set {
		prop, known := props[strings.ToLower(t.Key)]
		if !known {
			return nil, false
		}
		val := t.Value
		if isFlag(t.Key) {
			on, _ := parseBool(val)
			val = map[bool]string{true: "1", false: "0"}[on]
		}
		args = append(args, "--set", prop+"="+val)
	}
	for _, k := range e.Remove {
		prop, known := props[strings.ToLower(k)]
		switch {
		case !known:
			return nil, false
		case isFlag(k):
			args = append(args, "--set", prop+"=0")
		case prop == "language":
			args = append(args, "--set", "language=und") // Matroska treats a missing language as English
		default:
			args = append(args, "--delete", prop)
		}
	}
	return append([]string{"--quiet", path}, args...), true
}

// exiftoolArgs writes the edited copy of in to out. Copied tags come first so
// explicit settings override them.
func exiftoolArgs(in, out string, e Edit) []string {
	var args []string
	if e.CopyFrom != "" {
		args = append(args, "-tagsFromFile", e.CopyFrom, "-all:all")
	}
	for _, g := range e.RemoveGroups {
		args = append(args, "-"+g+":all=")
	}
	for _, t := range e.Set {
		args = append(args, "-"+t.Key+"="+t.Value)
	}
	for _, k := range e.Remove {
		args = append(args, "-"+k+"=")
	}
	// No -q: in -stay_open mode it also suppresses the {ready} marker.
	return append(args, "-o", out, in)
}
