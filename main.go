// hypr-encode inspects, converts and retags media files using ffmpeg tools.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/Mimic890/hypr-encode/pkg/meta"
	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// schemaVersion is bumped only on breaking changes to any --json output.
const schemaVersion = 1

// Exit codes. Documented in README.md.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitPartial     = 3
	exitMissingTool = 4
	exitInterrupted = 130
)

var exitNames = map[int]string{
	exitError:       "error",
	exitUsage:       "usage",
	exitPartial:     "partial",
	exitMissingTool: "tool_missing",
	exitInterrupted: "interrupted",
}

// codedError attaches an exit code to an error.
type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func usageErr(format string, a ...any) error {
	return &codedError{exitUsage, fmt.Errorf(format, a...)}
}

const usageText = `Usage: hypr-encode <command> [flags] [args]

Commands:
  info [path...]             Show media information (default command)
  convert <in...> -o <out>   Convert files with presets and a parallel queue
  preset <command>           List, show, save, remove or edit presets
  meta get|set|rm <file...>  Read or edit container, track and image tags
  doctor                     Check external tools, versions and GPU presets

hypr-encode <path...> is shorthand for hypr-encode info <path...>.
Run hypr-encode <command> -h for command flags.
`

type app struct {
	stdout, stderr io.Writer
	json           bool
	cores          int // CPU thread budget for conversions
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	a := &app{stdout: os.Stdout, stderr: os.Stderr, cores: runtime.NumCPU()}
	code := a.main(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// main runs one command, reports its error to stderr and returns the exit code.
func (a *app) main(ctx context.Context, args []string) int {
	for _, arg := range args {
		if arg == "--" {
			break
		}
		a.json = a.json || arg == "--json" || arg == "-json"
	}
	err := a.run(ctx, args)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return exitOK
	}

	code := exitError
	var ee *codedError
	switch {
	case ctx.Err() != nil:
		code, err = exitInterrupted, errors.New("interrupted")
	case errors.As(err, &ee):
		code = ee.code
	case errors.Is(err, tools.ErrMissing):
		code = exitMissingTool
	}

	if a.json {
		var line struct {
			SchemaVersion int `json:"schemaVersion"`
			Error         struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		line.SchemaVersion = schemaVersion
		line.Error.Code, line.Error.Message = exitNames[code], err.Error()
		json.NewEncoder(a.stderr).Encode(line)
	} else {
		fmt.Fprintln(a.stderr, anRed+"✖ "+safeText(err.Error())+anReset)
	}
	return code
}

func (a *app) run(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "info":
			return a.info(ctx, args[1:])
		case "doctor":
			return a.doctor(ctx, args[1:])
		case "convert":
			return a.convert(ctx, args[1:])
		case "preset":
			return a.preset(ctx, args[1:])
		case "meta":
			return a.meta(ctx, args[1:])
		case "help", "-h", "-help", "--help":
			fmt.Fprint(a.stdout, usageText)
			return nil
		}
	}
	return a.info(ctx, args)
}

// parseFlags parses flags placed anywhere among the arguments and returns the
// positional ones. "--" ends flag parsing. Every command accepts --json.
func (a *app) parseFlags(fs *flag.FlagSet, args []string, usage string) ([]string, error) {
	fs.SetOutput(io.Discard)
	fs.BoolVar(&a.json, "json", a.json, "print machine-readable JSON to stdout")
	var pos []string
	for {
		err := fs.Parse(args)
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(a.stdout, "Usage: hypr-encode %s\n\nFlags:\n", usage)
			fs.SetOutput(a.stdout)
			fs.PrintDefaults()
			return nil, err
		}
		if err != nil {
			return nil, usageErr("%s: %v", fs.Name(), err)
		}
		rest := fs.Args()
		consumed := args[:len(args)-len(rest)]
		if len(consumed) > 0 && consumed[len(consumed)-1] == "--" {
			return append(pos, rest...), nil
		}
		if len(rest) == 0 {
			return pos, nil
		}
		pos, args = append(pos, rest[0]), rest[1:]
	}
}

// writeJSON prints one indented JSON document to stdout.
func (a *app) writeJSON(v any) error {
	enc := json.NewEncoder(a.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// textOut returns stdout for human-readable output, without frames when piped.
func (a *app) textOut() io.Writer {
	if stdoutTTY {
		return a.stdout
	}
	return &framelessWriter{w: a.stdout}
}

// batchErr summarizes per-item failures: exit 3 if some items succeeded, 1 if none did.
func batchErr(verb string, failed, total int) error {
	if failed == 0 {
		return nil
	}
	err := fmt.Errorf("failed to %s %d of %d files", verb, failed, total)
	if failed < total {
		return &codedError{exitPartial, err}
	}
	return err
}

// ── info ──────────────────────────────────────────────────────────────────────

type infoDoc struct {
	SchemaVersion int         `json:"schemaVersion"`
	Files         []infoFile  `json:"files"`
	Errors        []itemError `json:"errors"`
}

// infoFile is ffprobe's view of a file plus, for images, every exiftool tag by group.
type infoFile struct {
	*probe.MediaInfo
	Exif map[string]map[string]any `json:"exif,omitempty"`
}

type itemError struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

func (a *app) info(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	args, err := a.parseFlags(fs, args, "info [--json] [file|directory...]")
	if err != nil {
		return err
	}
	if len(args) == 0 {
		args = []string{"."}
	}
	if _, err := tools.Path("ffprobe"); err != nil {
		return err
	}

	w := a.textOut()
	doc := infoDoc{SchemaVersion: schemaVersion, Files: []infoFile{}, Errors: []itemError{}}
	var paths []string
	for _, arg := range args {
		found, err := expandPath(arg)
		if err != nil {
			doc.Errors = append(doc.Errors, itemError{arg, err.Error()})
			continue
		}
		if len(found) == 0 && !a.json {
			fmt.Fprintf(w, "\n  %sNo media files found in %s%s\n\n", anGray, shortenPath(arg), anReset)
		}
		paths = append(paths, found...)
	}

	total := len(doc.Errors) + len(paths)
	report := func(e itemError) {
		// A single failure is reported once, as the command error.
		if !a.json && total > 1 {
			fmt.Fprintf(a.stderr, "  %s✖ %s: %s%s\n", anRed, safeText(filepath.Base(e.Path)), safeText(e.Error), anReset)
		}
	}
	for _, e := range doc.Errors {
		report(e)
	}

	// Image tags come from exiftool when it is installed, else from ffprobe.
	et := startExifTool(ctx, paths, meta.IsImage)
	if et != nil {
		defer et.Close()
	}

	detail := len(args) == 1 && len(paths) == 1 && paths[0] == args[0]
	if len(paths) > 0 && !detail && !a.json {
		fmt.Fprintln(w)
	}
	for _, path := range paths {
		pctx, cancel := context.WithTimeout(ctx, probe.DefaultTimeout)
		info, err := probe.ScanFileContext(pctx, path)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			doc.Errors = append(doc.Errors, itemError{path, err.Error()})
			report(doc.Errors[len(doc.Errors)-1])
			continue
		}
		file := infoFile{MediaInfo: info}
		if et != nil && isImageFile(info) {
			file.Exif, _ = et.ReadGroups(info.FilePath) // on failure the ffprobe tags remain
		}
		doc.Files = append(doc.Files, file)
		shown := withExifTags(info, file.Exif)
		switch {
		case a.json:
		case detail:
			printFileInfo(w, shown)
		default:
			printFileCard(w, shown, termWidth(), len(doc.Files))
			fmt.Fprintln(w)
		}
	}

	if a.json {
		if err := a.writeJSON(doc); err != nil {
			return err
		}
	}
	if total == 1 && len(doc.Errors) == 1 {
		return errors.New(doc.Errors[0].Error)
	}
	return batchErr("inspect", len(doc.Errors), total)
}

// expandPath returns the file itself, or the media files directly inside a directory.
func expandPath(path string) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return mediaPaths(path)
	}
	return []string{path}, nil
}

func mediaPaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || !isMediaFile(entry.Name()) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		fi, err := os.Stat(path)
		if err == nil && fi.Mode().IsRegular() {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func isMediaFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mp4", ".mov", ".avi", ".m4v", ".webm", ".wmv", ".mpg", ".mpeg",
		".ts", ".m2ts", ".mts", ".vob", ".mxf", ".flv", ".ogv", ".3gp",
		".mp3", ".flac", ".aac", ".opus", ".ogg", ".oga", ".wav", ".m4a",
		".aiff", ".aif", ".ape", ".wma", ".alac", ".ac3", ".dts",
		".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".tif", ".tiff",
		".avif", ".heic", ".heif":
		return true
	}
	return false
}
