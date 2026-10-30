package convert

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
	"github.com/Mimic890/hypr-encode/pkg/probe"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

// Job is one input file converted to one output file.
type Job struct {
	ID        int    `json:"jobId"`
	Input     string `json:"input"`
	Output    string `json:"output"`
	Preset    Preset `json:"-"`
	Overwrite bool   `json:"-"`
}

// Progress is a snapshot of a running ffmpeg job.
type Progress struct {
	OutTimeMs  int64   `json:"outTimeMs"`
	DurationMs int64   `json:"durationMs"` // 0 when unknown
	Percent    float64 `json:"percent"`    // 0 when the duration is unknown
	FPS        float64 `json:"fps"`
	Speed      float64 `json:"speed"`
	SizeBytes  int64   `json:"sizeBytes"`
}

// Run converts one file. ffmpeg writes to a hidden temp file next to the
// output, which is renamed into place only on success and removed otherwise.
// An existing output (including the input itself) is replaced only with job.Overwrite.
func Run(ctx context.Context, job Job, threads int, onProgress func(Progress)) (err error) {
	ffmpeg, err := tools.Path("ffmpeg")
	if err != nil {
		return err
	}
	if _, err := os.Stat(job.Output); err == nil && !job.Overwrite {
		return fmt.Errorf("%s %w", job.Output, fsutil.ErrExists)
	}
	src, err := Open(ctx, job.Input)
	if err != nil {
		return err
	}

	var durationMs int64
	var fps float64
	pctx, cancel := context.WithTimeout(ctx, probe.DefaultTimeout)
	if info, err := probe.ScanFileContext(pctx, job.Input); err == nil {
		durationMs = info.DurationMs
		for _, v := range info.Video {
			if !v.IsAttachedPic && fps == 0 {
				fps = v.FPS
			}
		}
	}
	cancel()

	if err := os.MkdirAll(filepath.Dir(job.Output), 0o755); err != nil {
		return err
	}
	tmp, err := fsutil.TempPath(job.Output)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	args, err := BuildArgs(job.Preset, src, tmp, threads)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	cmd.WaitDelay = 5 * time.Second
	var stderr, decStderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var dec *exec.Cmd
	var pr *os.File
	if src.Decoder != nil {
		dec = exec.CommandContext(ctx, src.Decoder[0], src.Decoder[1:]...)
		dec.WaitDelay = 5 * time.Second
		dec.Stderr = &decStderr
		var pw *os.File
		if pr, pw, err = os.Pipe(); err != nil {
			return err
		}
		dec.Stdout, cmd.Stdin = pw, pr
		err = dec.Start()
		pw.Close() // only the children may hold the pipe, so each sees the other exit
		if err != nil {
			pr.Close()
			return err
		}
		defer dec.Wait() // reaps the decoder on early returns
	}
	err = cmd.Start()
	if pr != nil {
		pr.Close()
	}
	if err != nil {
		return err
	}
	readProgress(stdout, durationMs, fps, onProgress)
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if dec != nil {
		if decErr := dec.Wait(); decErr != nil && decStderr.Len() > 0 {
			return fmt.Errorf("dcraw_emu failed on %q: %s", filepath.Base(job.Input), ffmpegError(decStderr.String(), decErr))
		}
	}
	if err != nil {
		return fmt.Errorf("ffmpeg failed on %q: %s", filepath.Base(job.Input), ffmpegError(stderr.String(), err))
	}
	if src.Grid != nil {
		w, h := src.size()
		if err := checkGridSize(ctx, tmp, w, h); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(job.Input), err)
		}
	}
	return fsutil.Commit(tmp, job.Output, job.Overwrite)
}

// checkGridSize refuses an output whose size differs from the tile grid's,
// so a single tile is never passed off as the whole image.
func checkGridSize(ctx context.Context, output string, w, h int) error {
	info, err := probe.ScanFileContext(ctx, output)
	if err != nil {
		return err
	}
	if len(info.Video) == 0 || info.Video[0].Width != w || info.Video[0].Height != h {
		got := "no image"
		if len(info.Video) > 0 {
			got = fmt.Sprintf("%dx%d", info.Video[0].Width, info.Video[0].Height)
		}
		return fmt.Errorf("output is %s but the tile grid is %dx%d; not writing a partial image", got, w, h)
	}
	return nil
}

// ffmpegError returns the last lines ffmpeg printed, which hold the actual reason.
func ffmpegError(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return err.Error()
	}
	return strings.Join(lines[max(len(lines)-3, 0):], "; ")
}

// readProgress parses ffmpeg -progress key=value blocks, calling fn once per block.
// ffmpeg reports out_time of the slowest stream, so a sparse subtitle track
// holds it back; with a known frame rate the video frame count is used too.
func readProgress(r io.Reader, durationMs int64, fps float64, fn func(Progress)) {
	p := Progress{DurationMs: durationMs}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		key, val, _ := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		switch key {
		case "out_time_us":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
				p.OutTimeMs = max(p.OutTimeMs, n/1000)
			}
		case "frame":
			if n, err := strconv.ParseInt(val, 10, 64); err == nil && fps > 0 {
				p.OutTimeMs = max(p.OutTimeMs, int64(float64(n)*1000/fps))
			}
		case "fps":
			p.FPS, _ = strconv.ParseFloat(val, 64)
		case "speed":
			p.Speed, _ = strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(val), "x"), 64)
		case "total_size":
			p.SizeBytes, _ = strconv.ParseInt(val, 10, 64)
		case "progress":
			if durationMs > 0 {
				p.Percent = math.Min(100, math.Round(float64(p.OutTimeMs)*1000/float64(durationMs))/10)
			}
			if val == "end" {
				p.Percent = 100
			}
			if fn != nil {
				fn(p)
			}
		}
	}
}

// Verify runs a short test encode with p to prove its encoders work on this
// machine; a listed encoder may still lack the hardware or driver behind it.
func Verify(ctx context.Context, p Preset) error {
	ffmpeg, err := tools.Path("ffmpeg")
	if err != nil {
		return err
	}
	available, err := listEncoders(ffmpeg)
	if err != nil {
		return err
	}
	for _, c := range []Codec{p.Video, p.Audio, p.Subtitle} {
		if !isCopy(c) && !available[c.Codec] {
			return fmt.Errorf("encoder %s is not included in this ffmpeg build", c.Codec)
		}
	}
	p.InputArgs = append(append([]string(nil), p.InputArgs...), "-f", "lavfi")
	p.Args = append(append([]string(nil), p.Args...), "-f", "null")
	p.Tracks = nil
	args, err := BuildArgs(p, Source{Path: "testsrc2=size=256x256:rate=10:duration=0.3"}, "-", 0)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return errors.New("test encode timed out")
		}
		return errors.New(ffmpegError(stderr.String(), err))
	}
	return nil
}

// listEncoders returns the encoder names ffmpeg was built with, cached per path.
func listEncoders(ffmpeg string) (map[string]bool, error) {
	encodersMu.Lock()
	defer encodersMu.Unlock()
	if encoders[ffmpeg] != nil {
		return encoders[ffmpeg], nil
	}
	out, err := exec.Command(ffmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg -encoders: %w", err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		// Lines look like " V....D libx264   libx264 H.264 ..."; the legend lines have "=" in field 2.
		if f := strings.Fields(line); len(f) >= 2 && len(f[0]) == 6 && f[1] != "=" {
			names[f[1]] = true
		}
	}
	encoders[ffmpeg] = names
	return names, nil
}

var (
	encodersMu sync.Mutex
	encoders   = map[string]map[string]bool{}
)

// VerifyGPU test-encodes every GPU-class preset concurrently.
// The result maps each GPU preset name to nil (works) or the failure.
func VerifyGPU(ctx context.Context, presets []Preset) map[string]error {
	results := map[string]error{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range presets {
		if p.Load != LoadGPU {
			continue
		}
		wg.Add(1)
		go func(p Preset) {
			defer wg.Done()
			err := Verify(ctx, p)
			mu.Lock()
			results[p.Name] = err
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return results
}

// Available resolves every preset in the library and test-encodes the GPU
// ones. It returns the presets usable here and each GPU preset's result.
func (l *Library) Available(ctx context.Context) ([]Preset, map[string]error, error) {
	var all []Preset
	for _, name := range l.Names() {
		p, err := l.Get(name)
		if err != nil {
			return nil, nil, err
		}
		all = append(all, p)
	}
	gpu := VerifyGPU(ctx, all)
	var ok []Preset
	for _, p := range all {
		if gpu[p.Name] == nil {
			ok = append(ok, p)
		}
	}
	return ok, gpu, ctx.Err()
}
