package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/term"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
	"github.com/Mimic890/hypr-encode/pkg/convert"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// stringList is a repeatable string flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// presetFlags are the codec flags shared by convert and preset save.
type presetFlags struct {
	fs                                     *flag.FlagSet
	vcodec, acodec, scodec, vbitrate       string
	abitrate, encPreset, pixFmt, container string
	load, nameTemplate                     string
	crf                                    int
	tracks, rawArgs                        stringList
}

func addPresetFlags(fs *flag.FlagSet) *presetFlags {
	f := &presetFlags{fs: fs}
	fs.StringVar(&f.vcodec, "vcodec", "", "video encoder, e.g. libx265, hevc_nvenc or copy")
	fs.StringVar(&f.acodec, "acodec", "", "audio encoder, e.g. libopus or copy")
	fs.StringVar(&f.scodec, "scodec", "", "subtitle encoder, e.g. copy or mov_text")
	fs.IntVar(&f.crf, "crf", 0, "constant quality for the main stream (mapped per encoder)")
	fs.StringVar(&f.vbitrate, "vbitrate", "", "video bitrate, e.g. 6M")
	fs.StringVar(&f.abitrate, "abitrate", "", "audio bitrate, e.g. 192k")
	fs.StringVar(&f.encPreset, "encoder-preset", "", "video encoder speed preset, e.g. slow or p5")
	fs.StringVar(&f.pixFmt, "pix-fmt", "", "video pixel format, e.g. yuv420p10le")
	fs.StringVar(&f.container, "container", "", "output extension when writing to a directory, e.g. mkv")
	fs.StringVar(&f.load, "load", "", "load class: cpu-heavy, light, gpu or io")
	fs.StringVar(&f.nameTemplate, "name-template", "", "output name template for directory output, e.g. {name}.{encoder}.{crf}")
	fs.Var(&f.tracks, "track", "track to keep: all, N, v, a, s, t, TYPE:N or TYPE:all (repeatable; default all)")
	fs.Var(&f.rawArgs, "ffmpeg-arg", "extra raw ffmpeg output argument (repeatable)")
	return f
}

// apply copies the flags the user set onto p. crf targets audio for audio presets.
func (f *presetFlags) apply(p *convert.Preset, media string) {
	f.fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "vcodec":
			p.Video.Codec = f.vcodec
		case "acodec":
			p.Audio.Codec = f.acodec
		case "scodec":
			p.Subtitle.Codec = f.scodec
		case "crf":
			crf := f.crf
			if media == "audio" {
				p.Audio.CRF = &crf
			} else {
				p.Video.CRF = &crf
			}
		case "vbitrate":
			p.Video.Bitrate = f.vbitrate
		case "abitrate":
			p.Audio.Bitrate = f.abitrate
		case "encoder-preset":
			p.Video.Preset = f.encPreset
		case "pix-fmt":
			p.Video.PixFmt = f.pixFmt
		case "container":
			p.Container = strings.TrimPrefix(f.container, ".")
		case "load":
			p.Load = f.load
		case "name-template":
			p.OutputTemplate = f.nameTemplate
		case "track":
			p.Tracks = f.tracks
		case "ffmpeg-arg":
			p.Args = append(append([]string(nil), p.Args...), f.rawArgs...)
		}
	})
}

func configPath(name string) (string, error) {
	dir, err := convert.ConfigDir()
	return filepath.Join(dir, name), err
}

func loadLibrary() (*convert.Library, error) {
	path, err := configPath("presets.yml")
	if err != nil {
		return nil, err
	}
	return convert.LoadLibrary(path)
}

// ── convert ───────────────────────────────────────────────────────────────────

type dryRunJob struct {
	convert.Job
	Preset  string   `json:"preset"`
	Load    string   `json:"load"`
	Threads int      `json:"threads"`
	Command []string `json:"command"`
}

func (a *app) convert(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("convert", flag.ContinueOnError)
	out := fs.String("o", "", "output file, or directory for several inputs (required)")
	presetName := fs.String("preset", "", "preset name (see preset list); flags override its fields")
	jobsFlag := "auto"
	fs.StringVar(&jobsFlag, "jobs", "auto", "parallel jobs: a number or auto (per load class, see config.yml)")
	fs.StringVar(&jobsFlag, "j", "auto", "shorthand for --jobs")
	failFast := fs.Bool("fail-fast", false, "stop all jobs after the first failure")
	onConflict := fs.String("on-conflict", "", "when an output exists: fail, overwrite or increment (default from config.yml, else fail)")
	overwrite := fs.Bool("overwrite", false, "replace existing output files; same as --on-conflict overwrite")
	dryRun := fs.Bool("dry-run", false, "print the ffmpeg commands without running them")
	pf := addPresetFlags(fs)
	paths, err := a.parseFlags(fs, args, "convert [flags] <input...> -o <output>")
	if err != nil {
		return err
	}
	switch {
	case len(paths) == 0 || *out == "":
		return usageErr("convert: need at least one input and -o <output>")
	case *overwrite && *onConflict != "" && *onConflict != convert.ConflictOverwrite:
		return usageErr("convert: --overwrite contradicts --on-conflict %s", *onConflict)
	case *overwrite:
		*onConflict = convert.ConflictOverwrite
	}
	jobs := 0
	if jobsFlag != "auto" {
		if jobs, err = strconv.Atoi(jobsFlag); err != nil || jobs < 1 {
			return usageErr("convert: --jobs must be auto or a positive number, got %q", jobsFlag)
		}
	}

	lib, err := loadLibrary()
	if err != nil {
		return err
	}
	p := convert.Preset{Name: "custom"}
	if *presetName != "" {
		if p, err = lib.Get(*presetName); errors.Is(err, convert.ErrUnknownPreset) {
			return usageErr("convert: %v", err)
		} else if err != nil {
			return err
		}
	}
	pf.apply(&p, p.Media)
	if err := p.Normalize(); err != nil {
		return usageErr("convert: %v", err)
	}

	var inputs []string
	for _, path := range paths {
		found, err := expandPath(path)
		if err != nil {
			return err
		}
		if fi, _ := os.Stat(path); fi.IsDir() {
			*out = filepath.Clean(*out) + string(filepath.Separator) // a directory input always means a directory output
		}
		inputs = append(inputs, found...)
	}
	if len(inputs) == 0 {
		return errors.New("no media files found in the given inputs")
	}
	cfgPath, err := configPath("config.yml")
	if err != nil {
		return err
	}
	cfg, err := convert.LoadConfig(cfgPath)
	if err != nil {
		return err
	}
	naming := cfg.Output
	if *onConflict != "" {
		naming.OnConflict = *onConflict
	}
	if pf.nameTemplate != "" {
		naming.Apply = convert.ApplyAlways // an explicit template is never ignored
	}
	planned, err := convert.Plan(inputs, *out, p, naming)
	if err != nil {
		return usageErr("convert: %v", err)
	}
	limits := cfg.Jobs
	cores := a.cores

	if *dryRun {
		limit := jobs
		if limit == 0 {
			limit = limits[p.Load]
		}
		threads := max(cores/limit, 1)
		var doc struct {
			SchemaVersion int         `json:"schemaVersion"`
			Jobs          []dryRunJob `json:"jobs"`
		}
		doc.SchemaVersion = schemaVersion
		for _, job := range planned {
			cmd, err := convert.BuildArgs(p, job.Input, job.Output, threads)
			if err != nil {
				return usageErr("convert: %v", err)
			}
			cmd = append([]string{"ffmpeg"}, cmd...)
			doc.Jobs = append(doc.Jobs, dryRunJob{job, p.Name, p.Load, threads, cmd})
			if !a.json {
				fmt.Fprintln(a.stdout, shellJoin(cmd))
			}
		}
		if a.json {
			return a.writeJSON(doc)
		}
		return nil
	}

	if _, err := tools.Path("ffmpeg"); err != nil {
		return err
	}
	q := &convert.Queue{Limits: limits, Cores: cores, FailFast: *failFast, OnEvent: a.convertEvents(len(planned))}
	q.SetJobs(jobs)
	errs := q.Run(ctx, planned)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failed := 0
	for _, err := range errs {
		if err != nil {
			failed++
		}
	}
	if len(errs) == 1 && errs[0] != nil {
		if errors.Is(errs[0], fsutil.ErrExists) {
			return fmt.Errorf("%w; pass --overwrite to replace it or --on-conflict increment to number it", errs[0])
		}
		return errs[0]
	}
	return batchErr("convert", failed, len(errs))
}

// convertEvents returns the event sink: JSON Lines on stdout with --json,
// otherwise a progress view on stderr.
func (a *app) convertEvents(total int) func(convert.Event) {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		return func(ev convert.Event) {
			enc.Encode(struct {
				SchemaVersion int `json:"schemaVersion"`
				convert.Event
			}{schemaVersion, ev})
		}
	}
	live := false
	if f, ok := a.stderr.(*os.File); ok {
		live = term.IsTerminal(int(f.Fd()))
	}
	v := &progressView{w: a.stderr, live: live, total: total, running: map[int]convert.Event{}}
	return v.handle
}

// progressView prints conversion events to stderr: on a terminal a live block with
// one line per running job; otherwise only finished jobs.
type progressView struct {
	mu                  sync.Mutex
	w                   io.Writer
	live                bool
	total, done, failed int
	running             map[int]convert.Event
	drawn               int
}

func (v *progressView) handle(ev convert.Event) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.drawn > 0 {
		fmt.Fprintf(v.w, "\x1b[%dA\r\x1b[J", v.drawn) // back to the top of the live block
		v.drawn = 0
	}
	name := safeText(filepath.Base(ev.Input))
	switch ev.Type {
	case "start", "progress":
		v.running[ev.JobID] = ev
	case "done":
		delete(v.running, ev.JobID)
		v.done++
		fmt.Fprintf(v.w, "  %s✔%s %s → %s\n", anGreen, anReset, name, safeText(ev.Output))
	case "failed", "canceled":
		delete(v.running, ev.JobID)
		v.failed++
		// A single job's error is reported once, as the command error.
		if ev.Error != "not started" && v.total > 1 {
			msg := ev.Error
			if ev.Type == "canceled" {
				msg = "canceled"
			}
			fmt.Fprintf(v.w, "  %s✖ %s: %s%s\n", anRed, name, safeText(msg), anReset)
		}
	}
	if !v.live || len(v.running) == 0 {
		return
	}

	width := 80
	if f, ok := v.w.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			width = w
		}
	}
	ids := make([]int, 0, len(v.running))
	for id := range v.running {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		fmt.Fprintln(v.w, visTrunc(progressLine(v.running[id]), width-1))
	}
	fmt.Fprintln(v.w, visTrunc(fmt.Sprintf("  %s%d/%d done, %d failed, %d running%s", anGray, v.done, v.total, v.failed, len(v.running), anReset), width-1))
	v.drawn = len(ids) + 1
}

func progressLine(ev convert.Event) string {
	line := fmt.Sprintf("  %s[%d]%s %s", anMagenta, ev.JobID, anReset, safeText(filepath.Base(ev.Input)))
	p := ev.Progress
	if p == nil {
		return line + anGray + "  starting…" + anReset
	}
	if p.DurationMs > 0 {
		filled := int(p.Percent / 5)
		line += fmt.Sprintf("  %s%s%s%s %5.1f%%", anTeal, strings.Repeat("█", filled), anDim+strings.Repeat("░", 20-filled), anReset, p.Percent)
	} else {
		line += "  " + fmtDurMs(p.OutTimeMs)
	}
	if p.Speed > 0 {
		line += fmt.Sprintf("  %s%.2fx%s", anGray, p.Speed, anReset)
	}
	return line
}

// shellJoin quotes args for copy-pasting into a POSIX shell.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, s := range args {
		safe := s != "" && strings.IndexFunc(s, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_./:=,+@%", r)
		}) < 0
		if safe {
			quoted[i] = s
		} else {
			quoted[i] = "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}
