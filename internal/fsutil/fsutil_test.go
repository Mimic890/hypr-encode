package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommit(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.mkv")

	tmp, err := TempPath(out)
	if err != nil || filepath.Dir(tmp) != dir || !strings.HasSuffix(tmp, ".mkv") || !strings.HasPrefix(filepath.Base(tmp), ".out.") {
		t.Fatalf("TempPath() = %q, %v", tmp, err)
	}
	os.WriteFile(tmp, []byte("new"), 0o600)
	if err := Commit(tmp, out, false); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", fi.Mode())
	}

	tmp2, _ := TempPath(out)
	if err := Commit(tmp2, out, false); !errors.Is(err, ErrExists) {
		t.Fatalf("Commit() without overwrite = %v, want ErrExists", err)
	}
	os.Chmod(out, 0o600)
	os.WriteFile(tmp2, []byte("newer"), 0o600)
	if err := Commit(tmp2, out, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(out); string(data) != "newer" {
		t.Fatalf("content = %q", data)
	}
	if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o600 {
		t.Fatalf("overwrite must keep mode, got %v", fi.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestTempPathRelative(t *testing.T) {
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
	tmp, err := TempPath("out.mp4")
	if err != nil || filepath.Dir(tmp) != "." {
		t.Fatalf("TempPath() = %q, %v; want it in the current directory", tmp, err)
	}
}
