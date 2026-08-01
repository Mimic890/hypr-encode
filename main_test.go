package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestParseTarget(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "."},
		{"path", []string{"movie.mkv"}, "movie.mkv"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseTarget(test.args, &bytes.Buffer{})
			if err != nil || got != test.want {
				t.Fatalf("parseTarget() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
	if _, err := parseTarget([]string{"a", "b"}, &bytes.Buffer{}); err == nil {
		t.Fatal("multiple paths must fail")
	}
	if _, err := parseTarget([]string{"--help"}, &bytes.Buffer{}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("help error = %v, want flag.ErrHelp", err)
	}
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
	if len(paths) != len(want) {
		t.Fatalf("mediaPaths() = %v, want %v", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("mediaPaths() = %v, want %v", paths, want)
		}
	}
}
