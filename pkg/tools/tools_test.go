package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := map[string]string{
		"ffmpeg version 6.1.1-3ubuntu5 Copyright (c) 2000-2023\nbuilt with gcc": "6.1.1-3ubuntu5",
		"ffprobe version n7.1 Copyright (c) 2007-2024":                          "n7.1",
		"mkvpropedit v82.0 ('I'm The President') 64-bit\n":                      "82.0",
		"12.76\n": "12.76",
	}
	for out, want := range tests {
		if got := parseVersion(out); got != want {
			t.Errorf("parseVersion(%q) = %q, want %q", out, got, want)
		}
	}
}

func TestMissingTool(t *testing.T) {
	t.Setenv("PATH", "")
	_, err := Path("ffprobe")
	if !errors.Is(err, ErrMissing) || err.Error() != "ffprobe not found in PATH; install FFmpeg" {
		t.Fatalf("Path() error = %v", err)
	}
	if st := Check(context.Background(), "ffprobe"); st.Found || st.Error == "" {
		t.Fatalf("Check() = %+v", st)
	}
}

func TestLibRawVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake simple_dcraw")
	}
	dir := t.TempDir()
	usage := "#!/bin/sh\necho 'simple_dcraw - LibRaw 0.22.3-Release sample. Emulates dcraw' >&2\nexit 1\n"
	os.WriteFile(filepath.Join(dir, "simple_dcraw"), []byte(usage), 0o755)
	if got := libRawVersion(context.Background(), filepath.Join(dir, "dcraw_emu")); got != "0.22.3" {
		t.Fatalf("libRawVersion() = %q", got)
	}
	if got := libRawVersion(context.Background(), filepath.Join(t.TempDir(), "dcraw_emu")); got != "" {
		t.Fatalf("without simple_dcraw: %q", got)
	}
}
