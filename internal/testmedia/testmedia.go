// Package testmedia generates tiny media files for integration tests.
package testmedia

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
