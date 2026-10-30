package tools

import (
	"context"
	"errors"
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
