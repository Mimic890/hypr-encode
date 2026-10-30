package convert

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
	"github.com/Mimic890/hypr-encode/internal/testmedia"
	"github.com/Mimic890/hypr-encode/pkg/probe"
)

func builtin(t *testing.T, name string) Preset {
	t.Helper()
	lib, err := LoadLibrary("")
	if err != nil {
		t.Fatal(err)
	}
	p, err := lib.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildArgs(t *testing.T) {
	head := "-hide_banner -nostdin -y -v error -progress pipe:1 -nostats "
	tests := []struct {
		preset  string
		threads int
		want    string
	}{
		{"h264", 4, "-threads 4 -i in.mkv -map 0 -c copy -c:V libx264 -crf:V 20 -preset:V medium -c:a aac -b:a 192k -threads 4 out.mkv"},
		{"hevc", 3, "-threads 3 -i in.mkv -map 0 -c copy -c:V libx265 -crf:V 22 -preset:V medium -x265-params pools=3 -c:a copy -threads 3 out.mkv"},
		{"av1", 0, "-i in.mkv -map 0 -c copy -c:V libsvtav1 -crf:V 30 -preset:V 6 -c:a copy out.mkv"},
		{"remux", 0, "-i in.mkv -map 0 -c copy -c:V copy -c:a copy out.mkv"},
		{"hevc-nvenc", 0, "-i in.mkv -map 0 -c copy -c:V hevc_nvenc -rc:V vbr -cq:V 24 -b:V 0 -preset:V p5 -c:a copy out.mkv"},
		{"av1-qsv", 0, "-i in.mkv -map 0 -c copy -c:V av1_qsv -global_quality:V 30 -preset:V medium -c:a copy out.mkv"},
		{"hevc-amf", 0, "-i in.mkv -map 0 -c copy -c:V hevc_amf -rc:V cqp -qp_i:V 24 -qp_p:V 24 -c:a copy out.mkv"},
		{"av1-vaapi", 0, "-vaapi_device /dev/dri/renderD128 -i in.mkv -map 0 -c copy -c:V av1_vaapi -rc_mode:V CQP -qp:V 120 -c:a copy -vf format=nv12,hwupload out.mkv"},
		{"mp3", 0, "-i in.mkv -map 0:a? -c copy -c:a libmp3lame -q:a 2 out.mkv"},
		{"opus", 0, "-i in.mkv -map 0:a? -c copy -c:a libopus -b:a 160k out.mkv"},
		{"webp", 0, "-i in.mkv -map 0:v:0 -c copy -c:V libwebp -quality:V 85 -frames:v 1 -update 1 out.mkv"},
		{"avif", 2, "-threads 2 -i in.mkv -map 0:v:0 -c copy -c:V libaom-av1 -crf:V 30 -b:V 0 -still-picture 1 -cpu-used 6 -threads 2 -frames:v 1 -update 1 out.mkv"},
	}
	for _, test := range tests {
		args, err := BuildArgs(builtin(t, test.preset), "in.mkv", "out.mkv", test.threads)
		if err != nil {
			t.Errorf("%s: %v", test.preset, err)
			continue
		}
		if got := strings.Join(args, " "); got != head+test.want {
			t.Errorf("%s:\n got %s\nwant %s", test.preset, got, head+test.want)
		}
	}

	p := builtin(t, "opus")
	crf := 5
	p.Audio.CRF = &crf
	if _, err := BuildArgs(p, "a", "b", 0); err == nil || !strings.Contains(err.Error(), "libopus") {
		t.Errorf("crf on libopus: err = %v", err)
	}
}

func TestMapArg(t *testing.T) {
	for sel, want := range map[string]string{"all": "0", "3": "0:3", "a": "0:a?", "s:all": "0:s?", "a:1": "0:a:1", "t": "0:t?"} {
		if got, err := mapArg(sel); err != nil || got != want {
			t.Errorf("mapArg(%q) = %q, %v; want %q", sel, got, err, want)
		}
	}
	for _, bad := range []string{"x", "a:-1", "v:first", "-1", ""} {
		if _, err := mapArg(bad); err == nil {
			t.Errorf("mapArg(%q) must fail", bad)
		}
	}
}

func TestBuiltinPresets(t *testing.T) {
	lib, err := LoadLibrary("")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"h264", "hevc", "av1", "remux",
		"hevc-nvenc", "av1-nvenc", "hevc-qsv", "av1-qsv", "hevc-amf", "av1-amf", "hevc-vaapi", "av1-vaapi",
		"opus", "aac", "mp3", "flac", "jpeg", "webp", "avif"}
	if !reflect.DeepEqual(lib.Names(), want) {
		t.Fatalf("Names() = %v", lib.Names())
	}
	nvenc := builtin(t, "hevc-nvenc")
	if nvenc.Media != "video" || nvenc.Container != "mkv" || nvenc.Load != LoadGPU || nvenc.Audio.Codec != "copy" || nvenc.Source != "builtin" {
		t.Fatalf("hevc-nvenc must inherit from hevc: %+v", nvenc)
	}
	if amf := builtin(t, "hevc-amf"); amf.Video.Preset != "" {
		t.Fatalf("hevc-amf must clear the x265 speed preset, got %q", amf.Video.Preset)
	}
	if remux := builtin(t, "remux"); remux.Load != LoadIO {
		t.Fatalf("remux load = %q", remux.Load)
	}
}

func TestUserPresets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presets.yml")
	os.WriteFile(path, []byte(`# my presets
- name: hevc       # override keeps the built-in as base
  extends: hevc
  video: {crf: 18}
- name: small
  extends: hevc
  tracks: [v, "a:0"]
`), 0o600)
	lib, err := LoadLibrary(path)
	if err != nil {
		t.Fatal(err)
	}
	hevc, _ := lib.Get("hevc")
	if *hevc.Video.CRF != 18 || hevc.Video.Codec != "libx265" || hevc.Source != "user" {
		t.Fatalf("override = %+v", hevc)
	}
	small, _ := lib.Get("small")
	if *small.Video.CRF != 18 || !reflect.DeepEqual(small.Tracks, []string{"v", "a:0"}) {
		t.Fatalf("small must extend the user hevc: %+v", small)
	}
	if nvenc, _ := lib.Get("hevc-nvenc"); *nvenc.Video.CRF != 24 {
		t.Fatalf("built-ins must not see user overrides: %+v", nvenc)
	}

	crf := 26
	if err := lib.Save(Preset{Name: "tiny", Extends: "small", Video: Codec{CRF: &crf}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Remove("hevc-nvenc"); err == nil {
		t.Fatal("removing a built-in must fail")
	}
	if err := lib.Remove("small"); err == nil {
		t.Fatal("removing a preset another one extends must fail")
	}

	lib, err = LoadLibrary(path)
	if err != nil {
		t.Fatal(err)
	}
	if tiny, _ := lib.Get("tiny"); *tiny.Video.CRF != 26 || tiny.Video.Codec != "libx265" {
		t.Fatalf("saved preset = %+v", tiny)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), "# my presets") {
		t.Fatalf("comments must survive a save:\n%s", data)
	}

	for name, body := range map[string]string{
		"unknown field": "- name: x\n  crff: 1\n",
		"bad load":      "- name: x\n  load: turbo\n",
		"cycle":         "- name: x\n  extends: y\n- name: y\n  extends: x\n",
		"missing base":  "- name: x\n  extends: nope\n",
		"duplicate":     "- name: x\n- name: x\n",
		"bad selector":  "- name: x\n  tracks: [q]\n",
	} {
		os.WriteFile(path, []byte(body), 0o600)
		if _, err := LoadLibrary(path); err == nil {
			t.Errorf("%s: LoadLibrary must fail", name)
		}
	}
}

func TestLoadLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	limits, err := LoadLimits(path)
	if err != nil || limits[LoadCPUHeavy] != 1 || limits[LoadGPU] != 2 {
		t.Fatalf("defaults = %v, %v", limits, err)
	}
	os.WriteFile(path, []byte("jobs:\n  cpu-heavy: 3\n"), 0o600)
	if limits, err := LoadLimits(path); err != nil || limits[LoadCPUHeavy] != 3 || limits[LoadIO] != 2 {
		t.Fatalf("limits = %v, %v", limits, err)
	}
	for _, bad := range []string{"jobs:\n  turbo: 1\n", "jobs:\n  io: 0\n", "job: {}\n"} {
		os.WriteFile(path, []byte(bad), 0o600)
		if _, err := LoadLimits(path); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
}

func TestPlan(t *testing.T) {
	dir := t.TempDir()
	p := Preset{Container: "opus"}
	jobs, err := Plan([]string{"a/x.flac", "b/y.mp3"}, dir, p, false)
	if err != nil || jobs[0].Output != filepath.Join(dir, "x.opus") || jobs[1].Output != filepath.Join(dir, "y.opus") || jobs[1].ID != 2 {
		t.Fatalf("Plan() = %+v, %v", jobs, err)
	}
	if jobs, _ := Plan([]string{"x.mkv"}, "out.mp4", p, false); jobs[0].Output != "out.mp4" {
		t.Fatalf("single file output = %q", jobs[0].Output)
	}
	if jobs, _ := Plan([]string{"x.mkv"}, "new/", Preset{}, false); jobs[0].Output != filepath.Join("new", "x.mkv") {
		t.Fatalf("trailing slash output = %q", jobs[0].Output)
	}
	if _, err := Plan([]string{"a/x.flac", "b/x.wav"}, dir, p, false); err == nil {
		t.Fatal("colliding outputs must fail")
	}
}

func TestReadProgress(t *testing.T) {
	// The second block's out_time lags behind the video (sparse subtitles); frame/fps wins.
	in := "frame=10\nfps=25.5\nout_time_us=500000\ntotal_size=2048\nspeed=1.5x\nprogress=continue\nframe=18\nout_time_us=N/A\nprogress=continue\nprogress=end\n"
	var got []Progress
	readProgress(strings.NewReader(in), 1000, 25, func(p Progress) { got = append(got, p) })
	want := []Progress{
		{OutTimeMs: 500, DurationMs: 1000, Percent: 50, FPS: 25.5, Speed: 1.5, SizeBytes: 2048},
		{OutTimeMs: 720, DurationMs: 1000, Percent: 72, FPS: 25.5, Speed: 1.5, SizeBytes: 2048},
		{OutTimeMs: 720, DurationMs: 1000, Percent: 100, FPS: 25.5, Speed: 1.5, SizeBytes: 2048},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("readProgress() = %+v", got)
	}
}

// ── Integration (skipped without ffmpeg) ─────────────────────────────────────

func TestQueueConvertsInParallel(t *testing.T) {
	src := testmedia.Generate(t, "src.mkv", "-c:v", "mpeg4", "-c:a", "flac")
	dir := filepath.Dir(src)
	broken := filepath.Join(dir, "broken.mkv")
	os.WriteFile(broken, []byte("junk"), 0o600)

	opus := builtin(t, "opus")
	inputs := []string{src, broken, src}
	jobs, err := Plan(inputs[:2], filepath.Join(dir, "out"), opus, false)
	if err != nil {
		t.Fatal(err)
	}
	jobs = append(jobs, Job{ID: 3, Input: src, Output: filepath.Join(dir, "out", "src.mkv"), Preset: builtin(t, "remux")})

	var mu sync.Mutex
	types := map[int][]string{}
	q := &Queue{Limits: DefaultLimits(), Cores: 4, OnEvent: func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		if ev.Type != "progress" {
			types[ev.JobID] = append(types[ev.JobID], ev.Type)
		}
	}}
	errs := q.Run(context.Background(), jobs)
	if errs[0] != nil || errs[1] == nil || errs[2] != nil {
		t.Fatalf("errs = %v", errs)
	}
	if !reflect.DeepEqual(types[1], []string{"start", "done"}) || !reflect.DeepEqual(types[2], []string{"start", "failed"}) {
		t.Fatalf("events = %v", types)
	}
	info, err := probe.ScanFile(filepath.Join(dir, "out", "src.opus"))
	if err != nil || len(info.Audio) != 1 || info.Audio[0].Codec != "opus" || len(info.Video) != 0 {
		t.Fatalf("opus output = %+v, %v", info, err)
	}
	assertNoTempFiles(t, filepath.Join(dir, "out"))

	// Without Overwrite an existing output is never replaced.
	err = Run(context.Background(), jobs[0], 0, nil)
	if !errors.Is(err, fsutil.ErrExists) {
		t.Fatalf("second run = %v, want ErrExists", err)
	}
}

func TestFailFastCancelsRemainingJobs(t *testing.T) {
	src := testmedia.Generate(t, "src.mkv")
	dir := filepath.Dir(src)
	broken := filepath.Join(dir, "broken.mkv")
	os.WriteFile(broken, []byte("junk"), 0o600)
	jobs, _ := Plan([]string{broken, src}, filepath.Join(dir, "out"), builtin(t, "remux"), false)

	q := &Queue{Limits: DefaultLimits(), Cores: 1, FailFast: true}
	q.SetJobs(1)
	errs := q.Run(context.Background(), jobs)
	if errs[0] == nil || !errors.Is(errs[1], context.Canceled) {
		t.Fatalf("errs = %v", errs)
	}
}

func TestCancelRemovesTempFile(t *testing.T) {
	src := testmedia.Generate(t, "src.mkv")
	out := filepath.Join(t.TempDir(), "out.mkv")
	p := builtin(t, "remux")
	p.InputArgs = []string{"-re"} // read at native speed so the job is still running when canceled

	ctx, cancel := context.WithCancel(context.Background())
	err := Run(ctx, Job{Input: src, Output: out, Preset: p}, 0, func(Progress) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("canceled job must not create the output")
	}
	assertNoTempFiles(t, filepath.Dir(out))
}

func TestVerify(t *testing.T) {
	testmedia.Generate(t, "skip-without-ffmpeg.wav")
	if err := Verify(context.Background(), builtin(t, "h264")); err != nil {
		t.Fatalf("h264 must work everywhere ffmpeg does: %v", err)
	}
	p := builtin(t, "h264")
	p.Video.Codec = "no_such_encoder"
	if err := Verify(context.Background(), p); err == nil {
		t.Fatal("unknown encoder must fail verification")
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
