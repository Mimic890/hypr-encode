package meta

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mimic890/hypr-encode/internal/fsutil"
	"github.com/Mimic890/hypr-encode/internal/testmedia"
	"github.com/Mimic890/hypr-encode/pkg/tools"
)

func TestBackend(t *testing.T) {
	tests := []struct {
		path    string
		edit    Edit
		inPlace bool
		want    string
	}{
		{"a.mkv", Edit{Set: []Tag{{"title", "x"}}}, true, BackendMkvpropedit},
		{"a.mkv", Edit{Set: []Tag{{"title", "x"}}}, false, BackendFFmpeg},
		{"a.mkv", Edit{Track: "a:0", Set: []Tag{{"language", "rus"}, {"forced", "1"}}}, true, BackendMkvpropedit},
		{"a.mkv", Edit{Set: []Tag{{"comment", "x"}}}, true, BackendFFmpeg}, // mkvpropedit cannot set arbitrary tags
		{"a.mp4", Edit{Set: []Tag{{"title", "x"}}}, true, BackendFFmpeg},
		{"a.mp4", Edit{RemoveGroups: []string{"GPS"}}, true, BackendExifTool},
		{"a.MOV", Edit{Set: []Tag{{"Keys:Make", "x"}}}, false, BackendExifTool},
		{"a.flac", Edit{Set: []Tag{{"artist", "x"}}}, true, BackendFFmpeg},
		{"a.JPG", Edit{Set: []Tag{{"Artist", "x"}}}, false, BackendExifTool},
	}
	for _, test := range tests {
		got, err := Backend(test.path, test.edit, test.inPlace)
		if err != nil || got != test.want {
			t.Errorf("Backend(%s, %+v, %v) = %q, %v; want %q", test.path, test.edit, test.inPlace, got, err, test.want)
		}
	}
	for _, bad := range []struct {
		path string
		edit Edit
	}{
		{"a.mkv", Edit{RemoveGroups: []string{"GPS"}}},
		{"a.jpg", Edit{Track: "v:0", Set: []Tag{{"title", "x"}}}},
		{"a.mp4", Edit{Track: "a:0", CopyFrom: "b.mp4"}},
	} {
		if _, err := Backend(bad.path, bad.edit, true); err == nil {
			t.Errorf("Backend(%s, %+v) must fail", bad.path, bad.edit)
		}
	}
}

func TestValidate(t *testing.T) {
	for _, e := range []Edit{
		{},
		{Set: []Tag{{"-o", "x"}}},
		{Set: []Tag{{"a b", "x"}}},
		{Track: "a:0", Set: []Tag{{"default", "maybe"}}},
		{Set: []Tag{{"forced", "1"}}},
		{Track: "t:0", Set: []Tag{{"title", "x"}}},
		{Track: "a", Set: []Tag{{"title", "x"}}},
		{RemoveGroups: []string{"GPS:all"}},
	} {
		if err := e.Validate(); err == nil {
			t.Errorf("Validate(%+v) must fail", e)
		}
	}
}

func TestArgs(t *testing.T) {
	e := Edit{Track: "s:1", Set: []Tag{{"language", "rus"}, {"forced", "yes"}, {"title", "Signs & Songs"}}, Remove: []string{"default"}}
	if got, want := strings.Join(ffmpegArgs("/in.mkv", "/out.mkv", e), " "),
		"-hide_banner -nostdin -y -v error -i /in.mkv -map 0 -c copy -default_mode passthrough -metadata:s:s:1 language=rus -metadata:s:s:1 title=Signs & Songs -disposition:s:1 +forced-default /out.mkv"; got != want {
		t.Errorf("ffmpegArgs:\n got %s\nwant %s", got, want)
	}
	if got, want := strings.Join(ffmpegArgs("/in.mp3", "/out.mp3", Edit{Set: []Tag{{"artist", "Ann"}}, Remove: []string{"comment"}}), " "),
		"-hide_banner -nostdin -y -v error -i /in.mp3 -map 0 -c copy -metadata artist=Ann -metadata comment= /out.mp3"; got != want {
		t.Errorf("ffmpegArgs container:\n got %s\nwant %s", got, want)
	}

	args, ok := mkvpropeditArgs("/a.mkv", e)
	if want := "--quiet /a.mkv --edit track:s2 --set language=rus --set flag-forced=1 --set name=Signs & Songs --set flag-default=0"; !ok || strings.Join(args, " ") != want {
		t.Errorf("mkvpropeditArgs:\n got %s\nwant %s", strings.Join(args, " "), want)
	}
	args, _ = mkvpropeditArgs("/a.mkv", Edit{Track: "2", Remove: []string{"language", "title"}})
	if want := "--quiet /a.mkv --edit track:3 --set language=und --delete name"; strings.Join(args, " ") != want {
		t.Errorf("mkvpropeditArgs by index:\n got %s\nwant %s", strings.Join(args, " "), want)
	}
	args, _ = mkvpropeditArgs("/a.mkv", Edit{Remove: []string{"title"}})
	if want := "--quiet /a.mkv --edit info --delete title"; strings.Join(args, " ") != want {
		t.Errorf("mkvpropeditArgs container: got %s", strings.Join(args, " "))
	}

	got := strings.Join(exiftoolArgs("/a.jpg", "/t.jpg", Edit{CopyFrom: "/b.jpg", RemoveGroups: []string{"GPS"}, Set: []Tag{{"XMP-dc:Title", "x"}}, Remove: []string{"Artist"}}), " ")
	if want := "-tagsFromFile /b.jpg -all:all -GPS:all= -XMP-dc:Title=x -Artist= -o /t.jpg /a.jpg"; got != want {
		t.Errorf("exiftoolArgs:\n got %s\nwant %s", got, want)
	}
	if cstr("a\\b\nc  ") != "#[CSTR]a\\\\b\\nc  \n" {
		t.Errorf("cstr = %q", cstr("a\\b\nc  "))
	}
}

// ── Integration (skipped without the tools) ──────────────────────────────────

func needTool(t *testing.T, name string) {
	t.Helper()
	if _, err := tools.Path(name); err != nil {
		t.Skip(err)
	}
}

func TestRemuxEdit(t *testing.T) {
	src := testmedia.Generate(t, "src.mp4", "-c:v", "mpeg4", "-c:a", "aac", "-metadata:s:a:0", "language=eng")
	needTool(t, "ffprobe")
	ctx := context.Background()
	out := filepath.Join(t.TempDir(), "out.mp4")
	before, _ := os.ReadFile(src)

	backend, err := Apply(ctx, src, out, Edit{Set: []Tag{{"title", "Demo"}}}, false, nil)
	if err != nil || backend != BackendFFmpeg {
		t.Fatalf("Apply() = %q, %v", backend, err)
	}
	if after, _ := os.ReadFile(src); string(after) != string(before) {
		t.Fatal("the source must stay untouched without in-place")
	}
	if _, err := Apply(ctx, src, out, Edit{Set: []Tag{{"title", "x"}}}, false, nil); !errors.Is(err, fsutil.ErrExists) {
		t.Fatalf("existing output without overwrite: %v", err)
	}
	if _, err := Apply(ctx, out, "", Edit{Track: "a:0", Set: []Tag{{"language", "rus"}}}, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, out, "", Edit{Track: "a:3", Set: []Tag{{"language", "rus"}}}, false, nil); err == nil {
		t.Fatal("a missing track must fail")
	}
	fm, err := Read(ctx, out, nil)
	if err != nil || fm.Tags["title"] != "Demo" || fm.Tracks[1].Selector != "a:0" || fm.Tracks[1].Tags["language"] != "rus" {
		t.Fatalf("Read() = %+v, %v", fm, err)
	}
	assertNoTemp(t, filepath.Dir(out))
}

func TestMkvpropeditEdit(t *testing.T) {
	src := testmedia.Generate(t, "src.mkv", "-c:v", "mpeg4", "-c:a", "flac")
	needTool(t, "mkvpropedit")
	ctx := context.Background()
	backend, err := Apply(ctx, src, "", Edit{Track: "a:0", Set: []Tag{{"title", "Dub"}, {"language", "rus"}, {"default", "0"}}}, false, nil)
	if err != nil || backend != BackendMkvpropedit {
		t.Fatalf("Apply() = %q, %v", backend, err)
	}
	if _, err := Apply(ctx, src, "", Edit{Set: []Tag{{"title", "Movie"}}}, false, nil); err != nil {
		t.Fatal(err)
	}
	fm, err := Read(ctx, src, nil)
	if err != nil || fm.Tags["title"] != "Movie" {
		t.Fatalf("Read() = %+v, %v", fm, err)
	}
	a := fm.Tracks[1]
	if a.Tags["title"] != "Dub" || a.Tags["language"] != "rus" || a.Default {
		t.Fatalf("audio track = %+v", a)
	}
}

func TestExifToolEdit(t *testing.T) {
	src := testmedia.Generate(t, "p.jpg")
	needTool(t, "exiftool")
	ctx := context.Background()
	et, err := StartExifTool(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer et.Close()

	dir := t.TempDir()
	tagged := filepath.Join(dir, "tagged.jpg")
	edit := Edit{Set: []Tag{{"Artist", "  Ann\nLee"}, {"GPSLatitude", "55.75"}, {"GPSLatitudeRef", "N"}, {"XMP-dc:Title", "T"}}}
	if backend, err := Apply(ctx, src, tagged, edit, false, et); err != nil || backend != BackendExifTool {
		t.Fatalf("Apply() = %q, %v", backend, err)
	}
	fm, err := Read(ctx, tagged, et)
	if err != nil || fm.Groups["IFD0"]["Artist"] != "  Ann\nLee" || fm.Groups["GPS"] == nil || fm.Groups["XMP-dc"]["Title"] != "T" {
		t.Fatalf("groups = %+v, %v", fm.Groups, err)
	}

	copied := filepath.Join(dir, "copied.jpg")
	if _, err := Apply(ctx, src, copied, Edit{CopyFrom: tagged, RemoveGroups: []string{"GPS"}}, false, et); err != nil {
		t.Fatal(err)
	}
	fm, _ = Read(ctx, copied, et)
	if fm.Groups["XMP-dc"]["Title"] != "T" || fm.Groups["GPS"] != nil {
		t.Fatalf("copy without GPS = %+v", fm.Groups)
	}

	if _, err := Apply(ctx, copied, "", Edit{Remove: []string{"XMP-dc:Title"}}, false, et); err != nil {
		t.Fatal(err)
	}
	fm, _ = Read(ctx, copied, et)
	if fm.Groups["XMP-dc"]["Title"] != nil {
		t.Fatalf("in-place removal failed: %+v", fm.Groups["XMP-dc"])
	}
	assertNoTemp(t, dir)
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("expected only tagged.jpg and copied.jpg (no *_original backups), got %v", entries)
	}

	if _, err := Apply(ctx, src, filepath.Join(dir, "bad.jpg"), Edit{Set: []Tag{{"NoSuchTagXyz", "1"}}}, false, et); err == nil {
		t.Fatal("an unknown exiftool tag must fail")
	}
}

func assertNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") || strings.HasSuffix(e.Name(), "_original") {
			t.Fatalf("leftover file %s", e.Name())
		}
	}
}
