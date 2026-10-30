// Package tools locates the external programs hypr-encode depends on
// and reports their versions.
package tools

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Known lists every external tool hypr-encode can use, in display order.
var Known = []string{"ffprobe", "ffmpeg", "mkvpropedit", "exiftool"}

var installHints = map[string]string{
	"ffprobe":     "install FFmpeg",
	"ffmpeg":      "install FFmpeg",
	"mkvpropedit": "install MKVToolNix",
	"exiftool":    "install ExifTool",
}

var versionArgs = map[string]string{
	"ffprobe":     "-version",
	"ffmpeg":      "-version",
	"mkvpropedit": "--version",
	"exiftool":    "-ver",
}

// ErrMissing matches any MissingError via errors.Is.
var ErrMissing = errors.New("required tool not found")

// MissingError reports that a required external tool is not installed.
type MissingError struct{ Name string }

func (e *MissingError) Error() string {
	msg := e.Name + " not found in PATH"
	if hint := installHints[e.Name]; hint != "" {
		msg += "; " + hint
	}
	return msg
}

func (e *MissingError) Is(target error) bool { return target == ErrMissing }

// Path returns the absolute path of a tool or a *MissingError.
func Path(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", &MissingError{Name: name}
	}
	return path, nil
}

// Status describes one external tool as found on this machine.
type Status struct {
	Name    string `json:"name"`
	Found   bool   `json:"found"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Check locates a tool and asks it for its version.
func Check(ctx context.Context, name string) Status {
	st := Status{Name: name}
	path, err := Path(name)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Found, st.Path = true, path

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, versionArgs[name]).Output()
	if err != nil {
		st.Error = "version check failed: " + err.Error()
		return st
	}
	st.Version = parseVersion(string(out))
	return st
}

// parseVersion extracts the version from the first line of a -version banner.
func parseVersion(out string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	f := strings.Fields(line)
	switch {
	case len(f) >= 3 && f[1] == "version": // ffmpeg, ffprobe
		return f[2]
	case len(f) >= 2 && strings.HasPrefix(f[1], "v"): // mkvpropedit
		return strings.TrimPrefix(f[1], "v")
	}
	return strings.TrimSpace(line) // exiftool prints the bare version
}
