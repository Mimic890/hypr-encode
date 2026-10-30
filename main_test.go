package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mimic890/hypr-encode/internal/testmedia"
	"github.com/Mimic890/hypr-encode/pkg/convert"
	"github.com/Mimic890/hypr-encode/pkg/meta"
	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

var update = flag.Bool("update", false, "rewrite golden files")

// testdata is absolute because some tests change the working directory.
var testdata, _ = filepath.Abs("testdata")

// golden compares got with testdata/<name>.golden, or rewrites it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join(testdata, name+".golden")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s mismatch; run go test -update\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

// runApp runs the CLI in-process and returns exit code, stdout and stderr.
func runApp(ctx context.Context, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := (&app{stdout: &stdout, stderr: &stderr, cores: 8}).main(ctx, args)
	return code, stdout.String(), stderr.String()
}

func TestParseFlagsInterspersed(t *testing.T) {
	a := &app{stdout: &bytes.Buffer{}}
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	out := fs.String("o", "", "")
	pos, err := a.parseFlags(fs, []string{"a", "--json", "b", "-o", "v", "--", "-c"}, "x")
	if err != nil || !reflect.DeepEqual(pos, []string{"a", "b", "-c"}) || *out != "v" || !a.json {
		t.Fatalf("parseFlags() = %v, %v; o=%q json=%v", pos, err, *out, a.json)
	}
}

func TestExitCodes(t *testing.T) {
	ctx := context.Background()
	if code, out, _ := runApp(ctx, "info", "-h"); code != exitOK || !strings.Contains(out, "Usage: hypr-encode info") {
		t.Errorf("help: code %d, stdout %q", code, out)
	}
	if code, _, _ := runApp(ctx, "info", "--bogus"); code != exitUsage {
		t.Errorf("unknown flag: code %d, want %d", code, exitUsage)
	}
	if code, _, _ := runApp(ctx, "doctor", "extra"); code != exitUsage {
		t.Errorf("extra arg: code %d, want %d", code, exitUsage)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if code, _, _ := runApp(canceled, "doctor"); code != exitInterrupted {
		t.Errorf("canceled: code %d, want %d", code, exitInterrupted)
	}

	t.Setenv("PATH", "")
	code, out, errOut := runApp(ctx, "--json", "missing.mkv")
	if code != exitMissingTool || out != "" {
		t.Errorf("missing ffprobe: code %d, stdout %q", code, out)
	}
	var line struct {
		SchemaVersion int
		Error         struct{ Code, Message string }
	}
	if err := json.Unmarshal([]byte(errOut), &line); err != nil || line.SchemaVersion != schemaVersion || line.Error.Code != "tool_missing" {
		t.Errorf("JSON error line = %q (%v)", errOut, err)
	}
}

func TestInfoJSONPartialFailure(t *testing.T) {
	good := testmedia.Generate(t, "good.mkv", "-metadata", "title=Demo")
	dir := filepath.Dir(good)
	if err := os.WriteFile(filepath.Join(dir, "broken.mp4"), []byte("not media"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.Path("ffprobe"); err != nil {
		t.Skip(err)
	}

	code, out, errOut := runApp(context.Background(), "info", dir, "--json")
	if code != exitPartial {
		t.Fatalf("code %d, want %d; stderr %s", code, exitPartial, errOut)
	}
	var doc infoDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != schemaVersion || len(doc.Files) != 1 || len(doc.Errors) != 1 {
		t.Fatalf("doc = %+v", doc)
	}
	f := doc.Files[0]
	if f.FileName != "good.mkv" || f.Tags["title"] != "Demo" || len(f.Video) != 1 || len(f.Audio) != 1 || f.Video[0].Width != 64 {
		t.Fatalf("file = %+v", f)
	}
	if filepath.Base(doc.Errors[0].Path) != "broken.mp4" {
		t.Fatalf("errors = %+v", doc.Errors)
	}
}

func TestInfoTextIsFramelessWhenPiped(t *testing.T) {
	file := testmedia.Generate(t, "clip.mkv")
	if _, err := tools.Path("ffprobe"); err != nil {
		t.Skip(err)
	}
	code, out, errOut := runApp(context.Background(), file)
	if code != exitOK || !strings.Contains(out, "clip.mkv") || strings.ContainsAny(out, "╭│╯─\x1b") {
		t.Fatalf("code %d, stderr %q, stdout:\n%s", code, errOut, out)
	}
}

func TestJSONGolden(t *testing.T) {
	var buf bytes.Buffer
	a := &app{stdout: &buf, json: true}

	a.writeJSON(infoDoc{
		SchemaVersion: schemaVersion,
		Files: []infoFile{{MediaInfo: &probe.MediaInfo{
			FilePath: "/media/demo.mkv", FileName: "demo.mkv", FileSize: 1048576, DurationMs: 10250,
			Format: "matroska,webm", FormatLong: "Matroska / WebM", BitrateBps: 818400, Chapters: 2,
			Tags: map[string]string{"title": "Demo"},
			Video: []probe.VideoStream{{
				Index: 0, Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, FPS: 23.976,
				PixFmt: "yuv420p10le", BitDepth: 10, Language: "und", Default: true, Tags: map[string]string{},
				HDR: probe.HDRInfo{IsHDR10: true, MaxCLL: 1000, ColorPrimaries: "bt2020", ColorTransfer: "smpte2084"},
			}},
			Audio: []probe.AudioStream{{Index: 1, Codec: "eac3", Channels: 6, ChannelLayout: "5.1(side)", SampleRate: 48000,
				Language: "eng", IsAtmos: true, Default: true, Tags: map[string]string{"language": "eng"}}},
			Subtitle: []probe.SubStream{{Index: 2, Codec: "subrip", Language: "rus", Forced: true, Tags: map[string]string{"language": "rus"}}},
		}}, {
			MediaInfo: &probe.MediaInfo{
				FilePath: "/media/photo.jpg", FileName: "photo.jpg", FileSize: 2048, Format: "image2", Tags: map[string]string{},
				Video: []probe.VideoStream{{Codec: "mjpeg", Width: 64, Height: 48, Tags: map[string]string{}}},
				Audio: []probe.AudioStream{}, Subtitle: []probe.SubStream{},
			},
			Exif: map[string]map[string]any{"IFD0": {"Make": "Canon", "Model": "EOS R6"}, "GPS": {"GPSLatitude": "55 deg 45' 0.00\" N"}},
		}},
		Errors: []itemError{{Path: "/media/broken.mp4", Error: "ffprobe failed"}},
	})
	golden(t, "info", buf.Bytes())

	buf.Reset()
	a.writeJSON(doctorDoc{SchemaVersion: schemaVersion, Tools: []tools.Status{
		{Name: "ffprobe", Found: true, Path: "/usr/bin/ffprobe", Version: "7.1"},
		{Name: "mkvpropedit", Error: "mkvpropedit not found in PATH; install MKVToolNix"},
	}, GPUPresets: []gpuStatus{
		{Name: "hevc-nvenc", Encoder: "hevc_nvenc", OK: true},
		{Name: "hevc-amf", Encoder: "hevc_amf", Error: "encoder hevc_amf is not included in this ffmpeg build"},
	}})
	golden(t, "doctor", buf.Bytes())

	buf.Reset()
	emit := a.convertEvents(2)
	emit(convert.Event{JobID: 1, Type: "start", Input: "a.mkv", Output: "out/a.mkv", Threads: 4})
	emit(convert.Event{JobID: 1, Type: "progress", Input: "a.mkv", Output: "out/a.mkv",
		Progress: &convert.Progress{OutTimeMs: 1500, DurationMs: 3000, Percent: 50, FPS: 120, Speed: 4.8, SizeBytes: 65536}})
	emit(convert.Event{JobID: 1, Type: "done", Input: "a.mkv", Output: "out/a.mkv"})
	emit(convert.Event{JobID: 2, Type: "failed", Input: "b.mkv", Output: "out/b.mkv", Error: "ffmpeg failed"})
	golden(t, "convert-events", buf.Bytes())

	buf.Reset()
	a.writeJSON(metaGetDoc{SchemaVersion: schemaVersion, Files: []*meta.FileMeta{{
		Path: "/media/demo.mkv", Tags: map[string]string{"title": "Demo"},
		Tracks: []meta.Track{
			{Index: 0, Selector: "v:0", Type: "video", Codec: "hevc", Default: true, Tags: map[string]string{}},
			{Index: 1, Selector: "s:0", Type: "subtitle", Codec: "subrip", Forced: true, Tags: map[string]string{"language": "rus"}},
		},
	}, {
		Path: "/media/photo.jpg", Tags: map[string]string{}, Tracks: []meta.Track{},
		Groups: map[string]map[string]any{"IFD0": {"Artist": "Ann"}, "XMP-dc": {"Title": "Sunset"}},
	}}, Errors: []itemError{}})
	golden(t, "meta-get", buf.Bytes())

	buf.Reset()
	a.writeJSON(metaEditDoc{SchemaVersion: schemaVersion, Results: []metaResult{
		{Path: "a.mkv", Output: "a.mkv", Backend: "mkvpropedit", Command: []string{"mkvpropedit", "--quiet", "/media/a.mkv", "--edit", "track:a1", "--set", "language=rus"}},
	}, Errors: []itemError{{Path: "b.mp4", Error: "b.mp4 has no track a:0"}}})
	golden(t, "meta-edit", buf.Bytes())
}

func TestMediaPaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.MP4", "a.mkv", ".hidden.mp4", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "folder.mp4"), 0o700); err != nil {
		t.Fatal(err)
	}

	paths, err := mediaPaths(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "a.mkv"), filepath.Join(dir, "b.MP4")}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("mediaPaths() = %v, want %v", paths, want)
	}
}

func TestInfoRawWithoutExifTool(t *testing.T) {
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed")
	}
	bin := t.TempDir() // ffprobe only, no exiftool
	if err := os.Symlink(ffprobe, filepath.Join(bin, "ffprobe")); err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", bin)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "shot.NEF"), []byte("raw"), 0o600)
	code, out, stderr := runApp(context.Background(), "info", "--json", dir)
	if code != exitOK || !strings.Contains(out, `"format": "raw"`) || !strings.Contains(out, "exiftool needed") {
		t.Fatalf("code %d, out %s, stderr %s", code, out, stderr)
	}
}
