package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mimic890/hypr-encode/internal/testmedia"
)

func TestMetaCommands(t *testing.T) {
	src := testmedia.Generate(t, "src.mp4", "-c:v", "mpeg4", "-c:a", "aac")
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip(err)
	}
	ctx := context.Background()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.mp4")

	if code, _, _ := runApp(ctx, "meta", "set", src, "title=x"); code != exitUsage {
		t.Errorf("without -o or --in-place: code %d", code)
	}
	if code, _, _ := runApp(ctx, "meta", "set", src, "-o", out, "--in-place", "title=x"); code != exitUsage {
		t.Errorf("with both -o and --in-place: code %d", code)
	}
	if code, _, _ := runApp(ctx, "meta", "set", src, "--in-place", "forced=1"); code != exitUsage {
		t.Errorf("a track flag without --track: code %d", code)
	}
	if code, _, errOut := runApp(ctx, "meta", "set", src, "-o", out, "--track", "a:0", "title=Main", "language=eng", "default=1"); code != exitOK {
		t.Fatalf("set: code %d, %s", code, errOut)
	}
	if code, _, errOut := runApp(ctx, "meta", "set", src, "-o", out, "title=Again"); code != exitError || !strings.Contains(errOut, "--overwrite") {
		t.Fatalf("existing output: code %d, %s", code, errOut)
	}
	if code, _, errOut := runApp(ctx, "meta", "set", out, "--in-place", "--track", "a:7", "title=x"); code != exitError || !strings.Contains(errOut, "no track a:7") {
		t.Fatalf("missing track: code %d, %s", code, errOut)
	}

	code, stdout, errOut := runApp(ctx, "meta", "get", out, "--json")
	if code != exitOK {
		t.Fatalf("get: code %d, %s", code, errOut)
	}
	var doc metaGetDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil || len(doc.Files) != 1 {
		t.Fatalf("get JSON: %v\n%s", err, stdout)
	}
	audio := doc.Files[0].Tracks[1]
	if audio.Selector != "a:0" || audio.Tags["language"] != "eng" || !audio.Default {
		t.Fatalf("audio track = %+v", audio)
	}

	if code, _, errOut := runApp(ctx, "meta", "rm", out, "--in-place", "--track", "a:0", "title"); code != exitOK {
		t.Fatalf("rm: code %d, %s", code, errOut)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestMetaMissingExifTool(t *testing.T) {
	img := testmedia.Generate(t, "p.jpg")
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip(err)
	}
	// A PATH holding only ffprobe: images need exiftool, which is now missing.
	bin := t.TempDir()
	os.Symlink(ffprobe, filepath.Join(bin, "ffprobe"))
	t.Setenv("PATH", bin)

	code, _, errOut := runApp(context.Background(), "meta", "set", img, "--in-place", "Artist=Ann")
	if code != exitMissingTool || !strings.Contains(errOut, "exiftool") {
		t.Fatalf("code %d, stderr %q", code, errOut)
	}
	// Reading falls back to ffprobe.
	if code, out, errOut := runApp(context.Background(), "meta", "get", img, "--json"); code != exitOK || strings.Contains(out, `"groups"`) {
		t.Fatalf("get without exiftool: code %d, %s %s", code, out, errOut)
	}
}
