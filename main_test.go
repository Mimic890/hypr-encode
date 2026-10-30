package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mimic890/hypr-encode/internal/testmedia"
	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

var update = flag.Bool("update", false, "rewrite golden files")

// golden compares got with testdata/<name>.golden, or rewrites it with -update.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
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
	code := (&app{stdout: &stdout, stderr: &stderr}).main(ctx, args)
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
	a := &app{stdout: &buf}

	a.writeJSON(infoDoc{
		SchemaVersion: schemaVersion,
		Files: []*probe.MediaInfo{{
			FilePath: "/media/demo.mkv", FileName: "demo.mkv", FileSize: 1048576, DurationMs: 10250,
			Format: "matroska,webm", FormatLong: "Matroska / WebM", BitrateBps: 818400, Chapters: 2,
			Tags: map[string]string{"title": "Demo"},
			Video: []probe.VideoStream{{
				Index: 0, Codec: "hevc", Profile: "Main 10", Width: 3840, Height: 2160, FPS: 23.976,
				PixFmt: "yuv420p10le", BitDepth: 10, Language: "und",
				HDR: probe.HDRInfo{IsHDR10: true, MaxCLL: 1000, ColorPrimaries: "bt2020", ColorTransfer: "smpte2084"},
			}},
			Audio:    []probe.AudioStream{{Index: 1, Codec: "eac3", Channels: 6, ChannelLayout: "5.1(side)", SampleRate: 48000, Language: "eng", IsAtmos: true}},
			Subtitle: []probe.SubStream{{Index: 2, Codec: "subrip", Language: "rus", Forced: true}},
		}},
		Errors: []itemError{{Path: "/media/broken.mp4", Error: "ffprobe failed"}},
	})
	golden(t, "info", buf.Bytes())

	buf.Reset()
	a.writeJSON(doctorDoc{SchemaVersion: schemaVersion, Tools: []tools.Status{
		{Name: "ffprobe", Found: true, Path: "/usr/bin/ffprobe", Version: "7.1"},
		{Name: "mkvpropedit", Error: "mkvpropedit not found in PATH; install MKVToolNix"},
	}})
	golden(t, "doctor", buf.Bytes())
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
