// Package testmedia generates tiny media files for integration tests.
package testmedia

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Generate writes a one-second lavfi clip named name into a temp dir and returns its path.
// The extension picks the inputs (audio only, one image frame, or video plus audio);
// extra args go before the output path. The test is skipped when ffmpeg is missing.
func Generate(t testing.TB, name string, extra ...string) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	video := []string{"-f", "lavfi", "-i", "testsrc2=size=64x48:rate=10:duration=1"}
	audio := []string{"-f", "lavfi", "-i", "sine=duration=1"}
	var args []string
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".flac", ".opus", ".ogg", ".m4a", ".wav":
		args = audio
	case ".jpg", ".jpeg", ".png", ".webp", ".avif", ".tif", ".tiff":
		args = append(video, "-frames:v", "1")
	default:
		args = append(video, audio...)
	}
	path := filepath.Join(t.TempDir(), name)
	args = append(append(append([]string{"-v", "error", "-y"}, args...), extra...), path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("generate %s: %v\n%s", name, err, out)
	}
	return path
}

// Fetch downloads a real sample file once into the user cache directory and
// returns its path. The test is skipped when the download fails, e.g. offline.
func Fetch(t testing.TB, url string) string {
	t.Helper()
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Skip(err)
	}
	dir = filepath.Join(dir, "hypr-encode-testdata")
	path := filepath.Join(dir, filepath.Base(url))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		t.Skipf("download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("download %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(dir, ".fetch-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Skipf("download %s: %v", url, err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		t.Fatal(err)
	}
	return path
}
