package convert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Event reports a job state change. Type is one of start, progress, done, failed, canceled.
type Event struct {
	JobID    int       `json:"jobId"`
	Type     string    `json:"type"`
	Input    string    `json:"input"`
	Output   string    `json:"output"`
	Threads  int       `json:"threads,omitempty"`
	Progress *Progress `json:"progress,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// Plan assigns an output path to every input. out names the output file for a
// single input, or a directory: one that exists, ends in a path separator, or
// is given with several inputs. Directory outputs are named by the name
// template (the preset's, else n.Template) when n.Apply allows it, and use the
// preset container as extension, or keep the input extension when the preset
// has none. An output that would replace its input is an error.
func Plan(inputs []string, out string, p Preset, n Naming) ([]Job, error) {
	if err := n.validate(); err != nil {
		return nil, err
	}
	tmpl := n.Template
	if p.OutputTemplate != "" {
		tmpl = p.OutputTemplate
	}
	dirOut := len(inputs) > 1 || strings.HasSuffix(out, "/") || strings.HasSuffix(out, string(filepath.Separator))
	if fi, err := os.Stat(out); err == nil && fi.IsDir() {
		dirOut = true
	}
	now := time.Now()
	jobs := make([]Job, 0, len(inputs))
	seen := map[string]string{}
	for i, in := range inputs {
		dst := out
		if dirOut {
			ext := filepath.Ext(in)
			name := strings.TrimSuffix(filepath.Base(in), ext)
			if p.Container != "" {
				ext = "." + p.Container
			}
			if tmpl != "" && (n.Apply == ApplyAlways || samePath(out, filepath.Dir(in))) {
				var err error
				if name, err = expandTemplate(tmpl, in, p, now); err != nil {
					return nil, err
				}
			}
			dst = filepath.Join(out, name+ext)
		}
		if samePath(dst, in) {
			return nil, fmt.Errorf("output %s would replace its input; pick another name or directory", dst)
		}
		taken := func(path string) bool {
			abs, _ := filepath.Abs(path)
			_, planned := seen[abs]
			_, err := os.Lstat(path)
			return planned || err == nil
		}
		for k, base := 1, dst; n.OnConflict == ConflictIncrement && taken(dst); k++ {
			dst = numbered(base, k)
		}
		abs, err := filepath.Abs(dst)
		if err != nil {
			return nil, err
		}
		if prev, ok := seen[abs]; ok {
			return nil, fmt.Errorf("%s and %s would both be written to %s", prev, in, dst)
		}
		seen[abs] = in
		jobs = append(jobs, Job{ID: i + 1, Input: in, Output: dst, Preset: p, Overwrite: n.OnConflict == ConflictOverwrite})
	}
	return jobs, nil
}

// DefaultLimits are the automatic per-class concurrency limits.
func DefaultLimits() map[string]int {
	return map[string]int{LoadCPUHeavy: 1, LoadLight: runtime.NumCPU(), LoadGPU: 2, LoadIO: 2}
}

// Queue runs jobs concurrently. With a fixed job count any job may take a free
// slot; in auto mode each load class has its own limit. Each job gets
// Cores divided by its concurrency limit as its CPU thread budget.
type Queue struct {
	Limits   map[string]int // auto-mode limits per load class
	Cores    int
	FailFast bool        // cancel the remaining jobs after the first failure
	OnEvent  func(Event) // called from one goroutine at a time

	mu      sync.Mutex
	jobs    int // fixed concurrency; 0 = auto
	wake    chan struct{}
	eventMu sync.Mutex
}

// SetJobs sets the fixed concurrency (0 = auto). Running jobs are not
// affected; the new value applies to jobs started afterwards.
func (q *Queue) SetJobs(n int) {
	q.mu.Lock()
	q.jobs = max(n, 0)
	q.mu.Unlock()
	q.signal()
}

// Jobs returns the fixed concurrency, or 0 in auto mode.
func (q *Queue) Jobs() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.jobs
}

func (q *Queue) signal() {
	q.mu.Lock()
	if q.wake == nil {
		q.wake = make(chan struct{}, 1)
	}
	wake := q.wake
	q.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (q *Queue) emit(ev Event) {
	if q.OnEvent == nil {
		return
	}
	q.eventMu.Lock()
	defer q.eventMu.Unlock()
	q.OnEvent(ev)
}

// limit returns the concurrency limit that applies to a load class. Callers hold q.mu.
func (q *Queue) limit(class string) int {
	if q.jobs > 0 {
		return q.jobs
	}
	return max(q.Limits[class], 1)
}

// Run executes all jobs and returns one error per job (nil on success).
// Jobs that never started because of cancellation get context.Canceled.
func (q *Queue) Run(ctx context.Context, jobs []Job) []error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	q.signal() // make sure q.wake exists

	errs := make([]error, len(jobs))
	started := make([]bool, len(jobs))
	running := map[string]int{}
	total := 0
	var wg sync.WaitGroup

	for ctx.Err() == nil {
		q.mu.Lock()
		pick := -1
		for i, job := range jobs {
			if started[i] {
				continue
			}
			if q.jobs > 0 {
				if total < q.jobs {
					pick = i
				}
				break // a fixed job count keeps strict queue order
			}
			if running[job.Preset.Load] < q.limit(job.Preset.Load) {
				pick = i
				break
			}
		}
		var threads int
		if pick >= 0 {
			class := jobs[pick].Preset.Load
			started[pick] = true
			running[class]++
			total++
			threads = max(q.Cores/q.limit(class), 1)
		}
		wake := q.wake
		q.mu.Unlock()

		if pick < 0 {
			if allStarted(started) {
				break
			}
			select {
			case <-wake:
			case <-ctx.Done():
			}
			continue
		}

		job := jobs[pick]
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.emit(Event{JobID: job.ID, Type: "start", Input: job.Input, Output: job.Output, Threads: threads})
			err := Run(ctx, job, threads, func(p Progress) {
				q.emit(Event{JobID: job.ID, Type: "progress", Input: job.Input, Output: job.Output, Progress: &p})
			})
			errs[pick] = err
			ev := Event{JobID: job.ID, Type: "done", Input: job.Input, Output: job.Output}
			switch {
			case err != nil && ctx.Err() != nil:
				ev.Type, ev.Error = "canceled", err.Error()
			case err != nil:
				ev.Type, ev.Error = "failed", err.Error()
				if q.FailFast {
					cancel()
				}
			}
			q.emit(ev)

			q.mu.Lock()
			running[job.Preset.Load]--
			total--
			q.mu.Unlock()
			q.signal()
		}()
	}

	wg.Wait()
	for i, job := range jobs {
		if !started[i] {
			errs[i] = context.Canceled
			q.emit(Event{JobID: job.ID, Type: "canceled", Input: job.Input, Output: job.Output, Error: "not started"})
		}
	}
	return errs
}

func allStarted(started []bool) bool {
	for _, s := range started {
		if !s {
			return false
		}
	}
	return true
}
