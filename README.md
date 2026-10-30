# hypr-encode

Terminal media toolkit powered by [FFmpeg](https://ffmpeg.org/).

It shows codecs, stream sizes, frame rate, HDR metadata, audio and subtitle tracks, language tags, and common media metadata without changing the inspected files.

## Install

Requirements: Go 1.21+ and [FFmpeg](https://ffmpeg.org/) (`ffprobe`, and `ffmpeg` for `convert`). Optional tools are listed by `hypr-encode doctor`.

```sh
go install github.com/Mimic890/hypr-encode@latest
```

Or build the repository locally:

```sh
go build -o hypr-encode .
```

## Usage

```text
hypr-encode <command> [flags] [args]
```

| Command | Purpose |
| --- | --- |
| `info [path...]` | Show media information (default command) |
| `convert <input...> -o <output>` | Convert files with presets and a parallel queue |
| `preset <command>` | List, show, save, remove or edit presets |
| `doctor` | Check external tools, their versions and GPU presets |

`hypr-encode <path...>` is shorthand for `hypr-encode info <path...>`. To inspect a file literally named like a command, use `hypr-encode info ./info`.

Flags may appear before or after positional arguments; `--` ends flag parsing. `-h` prints a command's flags. The CLI never asks questions interactively.

### info

```text
hypr-encode info [--json] [file|directory...]
```

| Invocation | Behavior |
| --- | --- |
| `hypr-encode` | Inspect media files in the current directory |
| `hypr-encode ./Movies` | Inspect media files in one directory |
| `hypr-encode file.mkv` | Show a detailed view of one file |
| `hypr-encode a.mkv b.mp4 ./Music` | Inspect several files and directories |

Directory scans are non-recursive, skip hidden entries and subdirectories, and discover media by file extension. Every file is attempted; failures are reported after the rest are shown (see exit codes).

Each `ffprobe` process has a 30-second timeout. Paths are passed as explicit local inputs, and filenames and metadata are sanitized before terminal output.

Output covers:

- File size, duration, overall bitrate, chapters, and common metadata
- Video codec, resolution, average frame rate, frame count, bit depth, pixel format, color range, bitrate, and stream size
- HDR10, HDR10+, HLG, and Dolby Vision data when reported by `ffprobe`
- Audio codec, channels, sample rate, bitrate, stream size, and detectable Atmos or DTS:X profiles
- Subtitle format, language, forced flag, title, and stream size
- Still-image dimensions, pixel format, bit depth, and common EXIF tags

### convert

```text
hypr-encode convert [flags] <input...> -o <output>
```

Inputs are files or directories (their media files, non-recursive). With one input file, `-o` is the output file. With several inputs, a directory input, an existing directory, or a path ending in `/`, `-o` is an output directory: each output is named after its input with the preset's container as extension. The directory is created if needed.

| Flag | Meaning |
| --- | --- |
| `-o PATH` | Output file or directory (required) |
| `--preset NAME` | Start from a preset; the flags below override its fields |
| `--vcodec`, `--acodec`, `--scodec` | Encoder per stream type, e.g. `libx265`, `hevc_nvenc`, `copy` |
| `--crf N` | Constant quality for the main stream (video, or audio for audio presets), mapped per encoder |
| `--vbitrate`, `--abitrate` | Bitrates, e.g. `6M`, `192k` |
| `--encoder-preset` | Video encoder speed preset, e.g. `slow`, `p5` |
| `--pix-fmt` | Video pixel format, e.g. `yuv420p10le` |
| `--track SEL` | Track to keep (repeatable): `all`, stream index `N`, `v`, `a`, `s`, `t`, `TYPE:N`, `TYPE:all`. Default: all tracks |
| `--container EXT` | Output extension for directory output |
| `--load CLASS` | Load class: `cpu-heavy`, `light`, `gpu`, `io` |
| `--ffmpeg-arg ARG` | Extra raw ffmpeg output argument (repeatable) |
| `-j N`, `--jobs N` | Parallel jobs: a number or `auto` (default) |
| `--fail-fast` | Cancel the remaining jobs after the first failure |
| `--overwrite` | Replace existing outputs, including the input itself |
| `--dry-run` | Print the ffmpeg commands and exit |

Without `--preset`, every track is copied (`-c copy`) and only the given flags change that. `--crf` maps to `-crf` (x264, x265, SVT-AV1, libaom, VP9), `-cq` (NVENC), `-global_quality` (QSV), `-qp` (VAAPI), `-qp_i`/`-qp_p` (AMF), `-quality` (WebP) or `-q` (JPEG, LAME); other encoders reject it.

Video encoders apply to video tracks only; attached pictures (cover art), attachments and other tracks are copied. Global metadata and chapters are kept by ffmpeg's defaults.

**Safety.** ffmpeg writes to a hidden temp file next to the output (`.name.*.part.ext`), which is renamed into place only after success. An existing output, including the input itself, is replaced only with `--overwrite`. ffmpeg runs without a shell and without stdin. Ctrl+C stops every running job, removes their temp files and exits with 130. Conversions have no timeout.

**Parallel queue.** Every preset has a load class. With `--jobs auto`, each class has its own limit and jobs of different classes run side by side; a light job may start while a cpu-heavy job waits. With `--jobs N`, at most N jobs of any class run, in queue order. Each job gets `cores / limit` CPU threads (`-threads`, plus `pools` for x265 and `lp` for SVT-AV1). A failed job does not stop the others unless `--fail-fast` is set.

| Load class | Auto limit |
| --- | --- |
| `cpu-heavy` | 1 |
| `light` | number of CPU cores |
| `gpu` | 2 |
| `io` | 2 |

**Progress.** Text mode writes progress and per-file results to stderr: a live block on a terminal, finished files only otherwise. With `--json`, stdout carries JSON Lines (see below).

Image inputs: ffmpeg reads JPEG, PNG, WebP, TIFF, BMP, GIF, AVIF and HEIC/HEIF. DNG is decoded only partially, and camera RAW formats (CR2, CR3, NEF, ARW, RAF, ORF) are not supported. Image conversion does not carry over EXIF metadata.

### preset

```text
hypr-encode preset list [--json]
hypr-encode preset show [--json] <name>
hypr-encode preset save [convert flags] [--preset BASE] [--description TEXT] [--media TYPE] [--yes] <name>
hypr-encode preset rm --yes <name>
hypr-encode preset edit
```

`list` hides GPU presets that fail a short test encode on this machine. `save` stores the given convert flags as a user preset; `--preset` becomes its `extends`. Replacing an existing user preset needs `--yes`, as does `rm`. `edit` opens the presets file in `$VISUAL` or `$EDITOR` and validates it afterwards.

Built-in presets (read-only):

| Media | Presets |
| --- | --- |
| Video | `h264` (x264 CRF 20, AAC), `hevc` (x265 CRF 22), `av1` (SVT-AV1 CRF 30), `remux` (copy) |
| Video, GPU | `hevc-nvenc`, `av1-nvenc`, `hevc-qsv`, `av1-qsv`, `hevc-amf`, `av1-amf`, `hevc-vaapi`, `av1-vaapi` |
| Audio | `opus` (160k), `aac` (256k, M4A), `mp3` (VBR V2), `flac` |
| Image | `jpeg`, `webp`, `avif` |

Video presets write MKV and copy audio unless noted. Audio presets keep audio tracks only. Image presets keep the first image. VAAPI presets use `/dev/dri/renderD128`.

### Configuration files

Both files live in the user config directory (`$XDG_CONFIG_HOME/hypr-encode/` or `~/.config/hypr-encode/` on Linux, `~/Library/Application Support/hypr-encode/` on macOS).

`presets.yml` holds a list of user presets. A user preset with a built-in's name overrides it for the CLI; built-in presets never see user overrides.

```yaml
- name: hevc-hq                  # lowercase letters, digits, '.', '_', '-'
  description: HEVC for archiving
  extends: hevc                  # inherit, then override the fields below
  media: video                   # video | audio | image
  container: mkv                 # extension for directory output
  load: cpu-heavy                # cpu-heavy | light | gpu | io
  video: {codec: libx265, crf: 18, preset: slow, pixFmt: yuv420p10le, args: [-tune, grain]}
  audio: {codec: libopus, bitrate: 192k}
  subtitle: {codec: copy}
  tracks: [v, a, "s:all"]        # default: all tracks
  inputArgs: [-hwaccel, cuda]    # raw args before -i
  args: [-movflags, +faststart]  # raw output args
```

`config.yml` changes the automatic job limits:

```yaml
jobs:
  cpu-heavy: 2
  gpu: 3
```

### doctor

```text
hypr-encode doctor [--json]
```

Checks `ffprobe`, `ffmpeg`, `mkvpropedit` and `exiftool`: whether each is in `PATH` and which version it reports. It also test-encodes a short clip with every GPU preset, since an encoder listed by ffmpeg may still lack the hardware or driver. Exits with code 4 if any tool is missing; GPU results do not affect the exit code. Commands that do not need a missing tool keep working.

## Terminal output

Colors and box frames are drawn only when stdout is a terminal. Piped output is plain text. Set [`NO_COLOR`](https://no-color.org/) to any non-empty value to disable colors in a terminal.

Data goes to stdout; errors and logs go to stderr.

## JSON output

Every command accepts `--json`. Documents are printed to stdout and always carry `schemaVersion`. The version changes only on breaking changes; new fields may be added without a bump.

`info --json`:

```json
{
  "schemaVersion": 1,
  "files": [
    {
      "filePath": "/media/demo.mkv",
      "fileName": "demo.mkv",
      "fileSize": 1048576,
      "durationMs": 10250,
      "format": "matroska,webm",
      "formatLong": "Matroska / WebM",
      "bitrateBps": 818400,
      "chapters": 2,
      "tags": { "title": "Demo" },
      "video": [ { "index": 0, "codec": "hevc", "width": 3840, "height": 2160, "fps": 23.976, "bitDepth": 10, "hdr": { "isHdr10": true } } ],
      "audio": [ { "index": 1, "codec": "eac3", "channels": 6, "language": "eng", "isAtmos": true } ],
      "subtitle": [ { "index": 2, "codec": "subrip", "language": "rus", "forced": true } ]
    }
  ],
  "errors": [ { "path": "/media/broken.mp4", "error": "ffprobe failed on \"broken.mp4\": ..." } ]
}
```

The stream objects are abbreviated above; the full field list is in [`testdata/info.golden`](testdata/info.golden). `files`, `errors`, `video`, `audio` and `subtitle` are always arrays, never `null`.

`doctor --json`:

```json
{
  "schemaVersion": 1,
  "tools": [
    { "name": "ffprobe", "found": true, "path": "/usr/bin/ffprobe", "version": "7.1" },
    { "name": "mkvpropedit", "found": false, "error": "mkvpropedit not found in PATH; install MKVToolNix" }
  ],
  "gpuPresets": [
    { "name": "hevc-nvenc", "encoder": "hevc_nvenc", "ok": true },
    { "name": "hevc-amf", "encoder": "hevc_amf", "ok": false, "error": "encoder hevc_amf is not included in this ffmpeg build" }
  ]
}
```

`convert --json` prints one event per line. Every line has `schemaVersion`, `jobId`, `type`, `input` and `output`. `type` is `start` (with `threads`), `progress` (with `progress`), `done`, `failed` or `canceled` (with `error`). `percent` and `durationMs` are 0 when the input duration is unknown.

```json
{"schemaVersion":1,"jobId":1,"type":"start","input":"a.mkv","output":"out/a.mkv","threads":4}
{"schemaVersion":1,"jobId":1,"type":"progress","input":"a.mkv","output":"out/a.mkv","progress":{"outTimeMs":1500,"durationMs":3000,"percent":50,"fps":120,"speed":4.8,"sizeBytes":65536}}
{"schemaVersion":1,"jobId":1,"type":"done","input":"a.mkv","output":"out/a.mkv"}
{"schemaVersion":1,"jobId":2,"type":"failed","input":"b.mkv","output":"out/b.mkv","error":"ffmpeg failed on \"b.mkv\": ..."}
```

`convert --dry-run --json` prints `{"schemaVersion":1,"jobs":[{"jobId","input","output","preset","load","threads","command":["ffmpeg",...]}]}`; see [`testdata/convert-dry-run.golden`](testdata/convert-dry-run.golden). The command shows the final output path; the real run writes to a temp file first.

`preset list --json` prints `{"schemaVersion":1,"presets":[...],"unavailable":[{"name","error"}]}`, and `preset show --json` prints `{"schemaVersion":1,"preset":{...}}` with the fields of `presets.yml` plus `source` (`builtin` or `user`); see [`testdata/preset-show.golden`](testdata/preset-show.golden).

On failure with `--json`, stderr receives one JSON line:

```json
{"schemaVersion":1,"error":{"code":"tool_missing","message":"ffprobe not found in PATH; install FFmpeg"}}
```

## Exit codes

| Code | `error.code` | Meaning |
| --- | --- | --- |
| 0 | | Success |
| 1 | `error` | The operation failed (for batches: every item failed) |
| 2 | `usage` | Invalid arguments or flags |
| 3 | `partial` | Some items of a batch failed, others succeeded |
| 4 | `tool_missing` | A required external tool is not installed |
| 130 | `interrupted` | Stopped by Ctrl+C or SIGTERM |

## Development

```sh
gofmt -w .
go vet ./...
go test ./...
go build ./...
```

Integration tests generate tiny media files with `ffmpeg -f lavfi` and are skipped when the needed tool is missing. Regenerate golden files with `go test . -update`. Tests point `XDG_CONFIG_HOME` and `HOME` at temp dirs, so they never touch your presets.

## License

MIT
