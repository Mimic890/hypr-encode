package meta

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// execTimeout bounds one exiftool command; the process is killed when it is exceeded.
const execTimeout = 60 * time.Second

// ExifTool is one exiftool process in -stay_open mode, shared by a whole batch.
// It is safe for concurrent use; commands run one at a time.
type ExifTool struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *bufio.Reader
}

// StartExifTool launches exiftool. Close it when the batch is done.
func StartExifTool(ctx context.Context) (*ExifTool, error) {
	path, err := tools.Path("exiftool")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, "-stay_open", "True", "-@", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &ExifTool{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: bufio.NewReader(stderr)}, nil
}

// cstr encodes one argument for exiftool's -@ argument file. The #[CSTR] form
// keeps leading whitespace and allows newlines inside values.
func cstr(arg string) string {
	return "#[CSTR]" + strings.NewReplacer(`\`, `\\`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(arg) + "\n"
}

// Exec runs one exiftool command and returns its stdout and stderr.
func (e *ExifTool) Exec(args ...string) (string, string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	timer := time.AfterFunc(execTimeout, func() { e.cmd.Process.Kill() })
	defer timer.Stop()

	var b strings.Builder
	for _, arg := range append(args, "-echo4", "{done}", "-execute") {
		b.WriteString(cstr(arg))
	}
	if _, err := io.WriteString(e.stdin, b.String()); err != nil {
		return "", "", fmt.Errorf("exiftool: %w", err)
	}
	// shortcut: stdout is drained before stderr, so a command printing over 64 KB of
	// errors would block; exiftool prints one line per problem, so batch errors stay small.
	stdout, err := readUntil(e.stdout, "{ready}")
	if err != nil {
		return "", "", fmt.Errorf("exiftool stopped: %w", err)
	}
	stderr, err := readUntil(e.stderr, "{done}")
	if err != nil {
		return "", "", fmt.Errorf("exiftool stopped: %w", err)
	}
	return stdout, stderr, nil
}

func readUntil(r *bufio.Reader, marker string) (string, error) {
	var b strings.Builder
	for {
		line, err := r.ReadString('\n')
		if strings.TrimRight(line, "\r\n") == marker {
			return b.String(), nil
		}
		b.WriteString(line)
		if err != nil {
			return b.String(), err
		}
	}
}

// Close ends the exiftool process.
func (e *ExifTool) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	io.WriteString(e.stdin, "-stay_open\nFalse\n")
	e.stdin.Close()
	return e.cmd.Wait()
}

// exifError returns the first "Error:" line exiftool printed, if any.
func exifError(stderr string) error {
	for _, line := range strings.Split(stderr, "\n") {
		if strings.HasPrefix(line, "Error") {
			return errors.New(strings.TrimSpace(line))
		}
	}
	return nil
}

// writeError is exifError plus the warnings exiftool prints when a requested
// tag was not written; it still exits 0 and copies the file unchanged then.
// Other warnings (such as a stale IPTC digest) do not fail the write.
func writeError(stderr string) error {
	if err := exifError(stderr); err != nil {
		return err
	}
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.HasPrefix(line, "Warning:") {
			continue
		}
		for _, s := range []string{"not defined", "not writable", "Error converting", "Can't"} {
			if strings.Contains(line, s) {
				return errors.New(strings.TrimSpace(line))
			}
		}
	}
	return nil
}

// ReadGroups returns every tag of a file, keyed by exiftool family 1 group
// (IFD0, ExifIFD, GPS, XMP-dc, IPTC, maker notes such as Canon, Composite...).
// path must be absolute so it can never be mistaken for an option.
func (e *ExifTool) ReadGroups(path string) (map[string]map[string]any, error) {
	stdout, stderr, err := e.Exec("-j", "-G1", "-s", path)
	if err != nil {
		return nil, err
	}
	if err := exifError(stderr); err != nil {
		return nil, fmt.Errorf("exiftool: %w", err)
	}
	var files []map[string]any
	if err := json.Unmarshal([]byte(stdout), &files); err != nil || len(files) != 1 {
		return nil, fmt.Errorf("exiftool: unexpected output for %s", path)
	}
	groups := map[string]map[string]any{}
	for key, val := range files[0] {
		group, tag, ok := strings.Cut(key, ":")
		if !ok {
			continue // SourceFile
		}
		if groups[group] == nil {
			groups[group] = map[string]any{}
		}
		groups[group][tag] = val
	}
	return groups, nil
}
