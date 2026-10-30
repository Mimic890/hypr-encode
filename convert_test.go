package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mimic890/hypr-encode/internal/testmedia"
)

// isolateConfig points the user config directory at a fresh temp dir.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir) // macOS ignores XDG_CONFIG_HOME
	return dir
}

// chdir switches to dir for the rest of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
}

func TestConvertDryRun(t *testing.T) {
	isolateConfig(t)
	chdir(t, t.TempDir())
	for _, name := range []string{"in.mkv", "my clip.mov"} {
		os.WriteFile(name, nil, 0o600)
	}
	ctx := context.Background()

	code, out, errOut := runApp(ctx, "convert", "in.mkv", "my clip.mov", "-o", "out", "--preset", "hevc", "--crf", "18", "--track", "v", "--track", "a:0", "--dry-run", "--json")
	if code != exitOK {
		t.Fatalf("code %d: %s", code, errOut)
	}
	golden(t, "convert-dry-run", []byte(out))

	code, out, _ = runApp(ctx, "convert", "my clip.mov", "-o", "x.mp4", "--vcodec", "libx264", "--dry-run", "-j", "2")
	want := "ffmpeg -hide_banner -nostdin -y -v error -progress pipe:1 -nostats -threads 4 -i 'my clip.mov' -map 0 -c copy -c:V libx264 -threads 4 x.mp4\n"
	if code != exitOK || out != want {
		t.Fatalf("text dry-run: code %d\n got %q\nwant %q", code, out, want)
	}
	if _, err := os.Stat("out"); !os.IsNotExist(err) {
		t.Fatal("dry-run must not create the output directory")
	}

	for _, args := range [][]string{
		{"convert", "in.mkv"},
		{"convert", "in.mkv", "-o", "x.mkv", "--preset", "nope"},
		{"convert", "in.mkv", "-o", "x.mkv", "-j", "0"},
		{"convert", "in.mkv", "-o", "x.mkv", "--preset", "opus", "--crf", "3"},
		{"convert", "in.mkv", "-o", "x.mkv", "--track", "q"},
	} {
		if code, _, _ := runApp(ctx, append(args, "--dry-run")...); code != exitUsage {
			t.Errorf("%v: code %d, want %d", args, code, exitUsage)
		}
	}
}

func TestConvertJSONLines(t *testing.T) {
	isolateConfig(t)
	src := testmedia.Generate(t, "src.mkv")
	broken := filepath.Join(filepath.Dir(src), "broken.mkv")
	os.WriteFile(broken, []byte("junk"), 0o600)
	out := filepath.Join(t.TempDir(), "out")

	code, stdout, stderr := runApp(context.Background(), "convert", src, broken, "-o", out, "--preset", "remux", "--json")
	if code != exitPartial {
		t.Fatalf("code %d, want %d; stderr %s", code, exitPartial, stderr)
	}
	final := map[int]string{}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	for sc.Scan() {
		var ev struct {
			SchemaVersion int
			JobID         int
			Type          string
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil || ev.SchemaVersion != schemaVersion || ev.JobID == 0 {
			t.Fatalf("bad event line %q: %v", sc.Text(), err)
		}
		final[ev.JobID] = ev.Type
	}
	if final[1] != "done" || final[2] != "failed" {
		t.Fatalf("final events = %v", final)
	}
	if !strings.Contains(stderr, `"code":"partial"`) {
		t.Fatalf("stderr = %q", stderr)
	}

	// The input itself is never replaced without --overwrite.
	code, _, stderr = runApp(context.Background(), "convert", src, "-o", src, "--preset", "remux")
	if code != exitError || !strings.Contains(stderr, "--overwrite") {
		t.Fatalf("convert onto the input: code %d, stderr %q", code, stderr)
	}
}

func TestPresetCommands(t *testing.T) {
	cfg := isolateConfig(t)
	ctx := context.Background()
	run := func(args ...string) (int, string) {
		code, out, errOut := runApp(ctx, args...)
		return code, out + errOut
	}

	if code, out := run("preset", "show", "hevc-nvenc", "--json"); code != exitOK {
		t.Fatalf("show: %d %s", code, out)
	} else {
		golden(t, "preset-show", []byte(out))
	}
	if code, out := run("preset", "save", "mine", "--preset", "opus", "--crf", "3", "--acodec", "libmp3lame", "--container", ".mp3"); code != exitOK {
		t.Fatalf("save: %d %s", code, out)
	}
	if code, _ := run("preset", "save", "mine", "--preset", "flac"); code != exitUsage {
		t.Fatalf("replacing without --yes: code %d", code)
	}
	if code, out := run("preset", "show", "mine", "--json"); code != exitOK || !strings.Contains(out, `"extends": "opus"`) || !strings.Contains(out, `"crf": 3`) || !strings.Contains(out, `"container": "mp3"`) {
		t.Fatalf("show mine: %d %s", code, out)
	}
	if code, out := run("preset", "list"); code != exitOK || !strings.Contains(out, "mine ") {
		t.Fatalf("list: %d %s", code, out)
	}
	if code, _ := run("preset", "rm", "mine"); code != exitUsage {
		t.Fatalf("rm without --yes: code %d", code)
	}
	if code, _ := run("preset", "rm", "hevc", "--yes"); code != exitUsage {
		t.Fatalf("rm builtin: code %d", code)
	}
	if code, out := run("preset", "rm", "mine", "--yes"); code != exitOK {
		t.Fatalf("rm: %d %s", code, out)
	}

	presets := filepath.Join(cfg, "hypr-encode", "presets.yml")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "true")
	os.Remove(presets)
	if code, out := run("preset", "edit"); code != exitOK {
		t.Fatalf("edit: %d %s", code, out)
	}
	if data, _ := os.ReadFile(presets); !strings.Contains(string(data), "hypr-encode user presets") {
		t.Fatalf("edit must create a template, got %q", data)
	}
	os.WriteFile(presets, []byte("- name: x\n  load: turbo\n"), 0o600)
	if code, out := run("preset", "edit"); code != exitError || !strings.Contains(out, "invalid") {
		t.Fatalf("edit with a broken file: %d %s", code, out)
	}
}
