package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
	"github.com/Mimic890/hypr-encode/pkg/convert"
	"github.com/Mimic890/hypr-encode/pkg/meta"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

const metaUsage = `Usage: hypr-encode meta <command> [flags]

Commands:
  get <file...>                 Show container, track and image tags
  set <file...> KEY=VALUE...    Write tags (needs -o or --in-place)
  rm <file...> KEY...           Delete tags (needs -o or --in-place)
`

func (a *app) meta(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageErr("meta: missing command\n\n%s", metaUsage)
	}
	switch args[0] {
	case "get":
		return a.metaGet(ctx, args[1:])
	case "set", "rm":
		return a.metaEdit(ctx, args[0], args[1:])
	case "help", "-h", "-help", "--help":
		fmt.Fprint(a.stdout, metaUsage)
		return nil
	}
	return usageErr("meta: unknown command %q\n\n%s", args[0], metaUsage)
}

// startExifTool starts one exiftool process when any path needs it and exiftool is
// installed. It returns nil otherwise, so callers fall back to ffprobe data.
func startExifTool(ctx context.Context, paths []string, need func(string) bool) *meta.ExifTool {
	if _, err := tools.Path("exiftool"); err != nil {
		return nil
	}
	for _, path := range paths {
		if need(path) {
			et, err := meta.StartExifTool(ctx)
			if err != nil {
				return nil
			}
			return et
		}
	}
	return nil
}

func (a *app) metaGet(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("meta get", flag.ContinueOnError)
	args, err := a.parseFlags(fs, args, "meta get [--json] <file|directory...>")
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return usageErr("meta get: expected at least one file")
	}
	if _, err := tools.Path("ffprobe"); err != nil {
		return err
	}
	var paths []string
	for _, arg := range args {
		found, err := expandPath(arg)
		if err != nil {
			return err
		}
		paths = append(paths, found...)
	}
	et := startExifTool(ctx, paths, meta.ReadsExif)
	if et != nil {
		defer et.Close()
	}

	doc := metaGetDoc{schemaVersion, []*meta.FileMeta{}, []itemError{}}
	w := a.textOut()
	for _, path := range paths {
		fm, err := meta.Read(ctx, path, et)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			doc.Errors = append(doc.Errors, itemError{path, err.Error()})
			if !a.json && len(paths) > 1 {
				fmt.Fprintf(a.stderr, "  %s✖ %s: %s%s\n", anRed, safeText(path), safeText(err.Error()), anReset)
			}
			continue
		}
		doc.Files = append(doc.Files, fm)
		if !a.json {
			printMeta(w, fm)
		}
	}
	if a.json {
		if err := a.writeJSON(doc); err != nil {
			return err
		}
	}
	if len(paths) == 1 && len(doc.Errors) == 1 {
		return errors.New(doc.Errors[0].Error)
	}
	return batchErr("read", len(doc.Errors), len(paths))
}

type metaGetDoc struct {
	SchemaVersion int              `json:"schemaVersion"`
	Files         []*meta.FileMeta `json:"files"`
	Errors        []itemError      `json:"errors"`
}

type metaEditDoc struct {
	SchemaVersion int          `json:"schemaVersion"`
	Results       []metaResult `json:"results"`
	Errors        []itemError  `json:"errors"`
}

type metaResult struct {
	Path    string   `json:"path"`
	Output  string   `json:"output"`
	Backend string   `json:"backend"`
	Command []string `json:"command,omitempty"`
}

func (a *app) metaEdit(ctx context.Context, verb string, args []string) error {
	fs := flag.NewFlagSet("meta "+verb, flag.ContinueOnError)
	out := fs.String("o", "", "write the result to this file, or directory for several inputs")
	inPlace := fs.Bool("in-place", false, "change the input files themselves")
	overwrite := fs.Bool("overwrite", false, "replace existing -o outputs")
	track := fs.String("track", "", "edit this track (v:N, a:N, s:N or stream index) instead of the container")
	dryRun := fs.Bool("dry-run", false, "print the backend and command without changing anything")
	var groups stringList
	from := ""
	usage := "meta set [flags] <file...> KEY=VALUE..."
	if verb == "set" {
		fs.StringVar(&from, "from", "", "copy all tags from this file first (images, MP4, MOV)")
	} else {
		usage = "meta rm [flags] <file...> KEY..."
		fs.Var(&groups, "group", "clear an exiftool tag group such as GPS or XMP (repeatable; images, MP4, MOV)")
	}
	args, err := a.parseFlags(fs, args, usage)
	if err != nil {
		return err
	}

	// Leading arguments that exist on disk are files; the rest are tags.
	var paths []string
	for len(args) > 0 {
		if _, err := os.Stat(args[0]); err != nil {
			break
		}
		paths, args = append(paths, args[0]), args[1:]
	}
	edit := meta.Edit{Track: *track, RemoveGroups: groups, CopyFrom: from}
	for _, arg := range args {
		if verb == "rm" {
			edit.Remove = append(edit.Remove, arg)
			continue
		}
		key, val, ok := strings.Cut(arg, "=")
		if !ok {
			return usageErr("meta set: %q is neither an existing file nor KEY=VALUE", arg)
		}
		edit.Set = append(edit.Set, meta.Tag{Key: key, Value: val})
	}
	if err := edit.Validate(); err != nil {
		return usageErr("meta %s: %v", verb, err)
	}
	switch {
	case len(paths) == 0:
		return usageErr("meta %s: expected at least one existing file", verb)
	case (*out == "") == !*inPlace:
		return usageErr("meta %s: pass either -o <output> or --in-place", verb)
	}

	outputs := make([]string, len(paths)) // "" means in place
	if *out != "" {
		naming := convert.Naming{}
		if *overwrite {
			naming.OnConflict = convert.ConflictOverwrite
		}
		jobs, err := convert.Plan(paths, *out, convert.Preset{}, naming)
		if err != nil {
			return usageErr("meta %s: %v", verb, err)
		}
		for i, job := range jobs {
			outputs[i] = job.Output
		}
	}

	// Check every needed tool up front so a missing one fails fast with exit code 4.
	needExif := false
	for _, path := range paths {
		backend, err := meta.Backend(path, edit, *inPlace)
		if err != nil {
			return usageErr("meta %s: %v", verb, err)
		}
		if _, err := tools.Path(backend); err != nil && !*dryRun {
			return err
		}
		needExif = needExif || backend == meta.BackendExifTool
	}
	var et *meta.ExifTool
	if needExif && !*dryRun {
		if et, err = meta.StartExifTool(ctx); err != nil {
			return err
		}
		defer et.Close()
	}

	doc := metaEditDoc{schemaVersion, []metaResult{}, []itemError{}}
	var firstErr error
	for i, path := range paths {
		res := metaResult{Path: path, Output: outputs[i]}
		if res.Output == "" {
			res.Output = path
		}
		if *dryRun {
			res.Backend, res.Command, err = meta.Command(ctx, path, outputs[i], edit)
		} else {
			res.Backend, err = meta.Apply(ctx, path, outputs[i], edit, *overwrite, et)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if errors.Is(err, fsutil.ErrExists) {
				err = fmt.Errorf("%w; pass --overwrite to replace it", err)
			}
			if firstErr == nil {
				firstErr = err
			}
			doc.Errors = append(doc.Errors, itemError{path, err.Error()})
			if !a.json && len(paths) > 1 {
				fmt.Fprintf(a.stderr, "  %s✖ %s: %s%s\n", anRed, safeText(path), safeText(err.Error()), anReset)
			}
			continue
		}
		doc.Results = append(doc.Results, res)
		switch {
		case a.json:
		case *dryRun:
			fmt.Fprintln(a.stdout, shellJoin(res.Command))
		default:
			fmt.Fprintf(a.stderr, "  %s✔%s %s → %s %s(%s)%s\n", anGreen, anReset, safeText(path), safeText(res.Output), anGray, res.Backend, anReset)
		}
	}
	if a.json {
		if err := a.writeJSON(doc); err != nil {
			return err
		}
	}
	if len(paths) == 1 && firstErr != nil {
		return firstErr
	}
	return batchErr("update", len(doc.Errors), len(paths))
}
