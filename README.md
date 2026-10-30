# hypr-encode

Terminal media toolkit powered by [FFmpeg](https://ffmpeg.org/).

- **Inspect** codecs, stream sizes, frame rate, HDR metadata, audio and subtitle tracks, language tags and image EXIF data.
- **Convert** files with built-in or custom presets, including GPU encoders, in a parallel queue.
- **Edit metadata** of containers, tracks and photos with `mkvpropedit`, `exiftool` or an `ffmpeg` remux.

Every command has `--json` output with a versioned schema and documented exit codes, so scripts and agents can drive it.

## Install

Requirements: Go 1.26+ and [FFmpeg](https://ffmpeg.org/) (`ffprobe`, and `ffmpeg` for `convert` and `meta`). Optional: [MKVToolNix](https://mkvtoolnix.download/) (`mkvpropedit`) for in-place Matroska edits [ExifTool](https://exiftool.org/) for image metadata, and [LibRaw](https://www.libraw.org/download)'s `dcraw_emu` for camera RAW photos (`apt install libraw-bin`, or `bin/` of the LibRaw Windows zip). FFmpeg 7.1+ is needed for tiled HEIF/AVIF photos. `hypr-encode doctor` lists what is installed.

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
| `meta get\|set\|rm <file...>` | Read or edit container, track and image tags |
| `doctor` | Check external tools, their versions and GPU presets |
| `tui [dir]` | Open the terminal interface |

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
- Still-image dimensions, pixel format, bit depth, and camera, lens, exposure and GPS tags

For images, tags come from `exiftool` when it is installed (EXIF, IPTC, XMP, GPS and maker notes; one exiftool process serves the whole run) and from `ffprobe` otherwise.

### convert

```text
hypr-encode convert [flags] <input...> -o <output>
```

Inputs are files or directories (their media files, non-recursive). With one input file, `-o` is the output file. With several inputs, a directory input, an existing directory, or a path ending in `/`, `-o` is an output directory: each output is named after its input (see [Output names](#output-names)) with the preset's container as extension. The directory is created if needed.

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
| `--name-template T` | Output name template for directory output, applied in any directory (see below) |
| `--ffmpeg-arg ARG` | Extra raw ffmpeg output argument (repeatable) |
| `-j N`, `--jobs N` | Parallel jobs: a number or `auto` (default) |
| `--fail-fast` | Cancel the remaining jobs after the first failure |
| `--on-conflict P` | When an output exists: `fail`, `overwrite` or `increment`. Default from `config.yml`, else `fail` |
| `--overwrite` | Same as `--on-conflict overwrite` |
| `--dry-run` | Print the ffmpeg commands and exit |

Without `--preset`, every track is copied (`-c copy`) and only the given flags change that. `--crf` maps to `-crf` (x264, x265, SVT-AV1, libaom, VP9), `-cq` (NVENC), `-global_quality` (QSV), `-qp` (VAAPI), `-qp_i`/`-qp_p` (AMF), `-quality` (WebP) or `-q` (JPEG, LAME); other encoders reject it.

Video encoders apply to video tracks only; attached pictures (cover art), attachments and other tracks are copied. Global metadata and chapters are kept by ffmpeg's defaults.

**Safety.** ffmpeg writes to a hidden temp file next to the output (`.name.*.part.ext`), which is renamed into place only after success. An existing output is replaced only with `--on-conflict overwrite` (or `--overwrite`). An output that would replace its own input is always an error. ffmpeg runs without a shell and without stdin. Ctrl+C stops every running job, removes their temp files and exits with 130. Conversions have no timeout.

#### Output names

Outputs in a directory are named by a template. By default (`{name}.conv`, applied only when the output directory is the input's directory) `movie.mkv` converted next to itself becomes `movie.conv.mkv`, while `-o out/` keeps `out/movie.mkv`. The extension always comes from the preset's container, or the input's when the preset has none. A single input with an explicit output file (`-o new.mp4`) is not renamed.

| Placeholder | Value |
| --- | --- |
| `{name}` | Input file name without extension |
| `{preset}` | Preset name (`custom` without `--preset`) |
| `{vcodec}`, `{acodec}` | Output codec, e.g. `hevc`, `opus` |
| `{encoder}` | Encoder of the main stream with `lib` dropped, e.g. `x265`, `hevc_nvenc`, `opus`; `copy` when copied |
| `{res}` | Video height, e.g. `1080p` |
| `{bitdepth}` | Video bit depth, from `--pix-fmt` when set |
| `{fps}` | Frame rate, e.g. `25`, `23.976` (empty for images) |
| `{crf}` | Constant quality of the main stream |
| `{date}` | Today, `YYYY-MM-DD` |

Values describe the output; a copied track takes its value from the input (ffprobe). The template is split at dots, and a segment whose placeholder is empty is dropped: with `{name}.{encoder}.{bitdepth}bit.{fps}fps`, `file1.mkv` becomes `file1.x265.10bit.25fps.mkv` with `hevc`, and `song.opus.opus` with `opus` (no video, so the bit depth and fps segments go). `/`, `\` and control characters are removed from values. `{res}`, `{fps}` and `{bitdepth}` come from the first video track and do not account for scaling or a pixel format set with `--ffmpeg-arg`.

The template is taken from `--name-template`, else the preset's `outputTemplate`, else `config.yml`. With `--on-conflict increment`, an existing `file1.conv.mkv` makes the output `file1.conv.1.mkv`, then `file1.conv.2.mkv`. `--dry-run` and `--json` show the final names.

**Parallel queue.** Every preset has a load class. With `--jobs auto`, each class has its own limit and jobs of different classes run side by side; a light job may start while a cpu-heavy job waits. With `--jobs N`, at most N jobs of any class run, in queue order. Each job gets `cores / limit` CPU threads (`-threads`, plus `pools` for x265 and `lp` for SVT-AV1). A failed job does not stop the others unless `--fail-fast` is set.

| Load class | Auto limit |
| --- | --- |
| `cpu-heavy` | 1 |
| `light` | number of CPU cores |
| `gpu` | 2 |
| `io` | 2 |

**Progress.** Text mode writes progress and per-file results to stderr: a live block on a terminal, finished files only otherwise. With `--json`, stdout carries JSON Lines (see below).

**Photos.** ffmpeg reads JPEG, PNG, WebP, TIFF, BMP, GIF and single-image HEIC/HEIF/AVIF itself. Tiled HEIF/AVIF images, which include every iPhone photo, are stored as a grid of small tiles: ffmpeg 8.0 outputs only the first tile, so hypr-encode reads the grid with `ffprobe -show_stream_groups` (FFmpeg 7.1+) and stacks and crops the tiles itself. An output whose size differs from the grid is an error, never a single tile. ffmpeg 8.0 ignores the rotation of HEIF photos (`irot` and EXIF Orientation), so a rotated photo comes out unrotated. Camera RAW and DNG files (CR2, CR3, NEF, ARW, RAF, ORF, RW2, PEF, SRW and other LibRaw formats) are decoded by `dcraw_emu` with the camera white balance and piped to ffmpeg as 16-bit PPM; without it they fail with a clear error. Directory inputs and the TUI list only formats ffmpeg reads, so RAW files must be given by name. Image conversion does not carry over EXIF metadata.

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
  outputTemplate: "{name}.{crf}" # replaces output.template of config.yml
```

`config.yml` changes the automatic job limits and output naming (defaults shown for `output`):

```yaml
jobs:
  cpu-heavy: 2
  gpu: 3
output:
  template: "{name}.conv"  # see Output names
  apply: same-dir          # same-dir: only when writing into the input's directory | always
  on_conflict: fail        # fail | overwrite | increment
```

### meta

```text
hypr-encode meta get [--json] <file|directory...>
hypr-encode meta set [flags] <file...> KEY=VALUE...
hypr-encode meta rm  [flags] <file...> KEY...
```

`get` shows container tags, every track with its selector (`v:0`, `a:1`, `s:0`), `default`/`forced` flags and tags, and, for images, MP4 and MOV, all exiftool tags by group (`IFD0`, `ExifIFD`, `GPS`, `XMP-dc`, `IPTC`, maker notes, ...).

`set` and `rm` take the files first (arguments that exist on disk), then the tags. They never change an input unless `--in-place` is given; otherwise `-o` names the output file, or a directory for several inputs.

| Flag | Meaning |
| --- | --- |
| `-o PATH` | Write the result here; the input stays untouched |
| `--in-place` | Change the input files themselves |
| `--overwrite` | Replace an existing `-o` output |
| `--track SEL` | Edit a track (`v:N`, `a:N`, `s:N` or stream index) instead of the container |
| `--from FILE` | `set` only: copy all tags from FILE first (images, MP4, MOV) |
| `--group NAME` | `rm` only: clear an exiftool group such as `GPS` or `XMP` (repeatable; images, MP4, MOV) |
| `--dry-run` | Print the backend command without changing anything |

The keys `default` and `forced` set track flags and take `1`/`0`, `yes`/`no` or `true`/`false`. Other keys are tag names: `title` and `language` for tracks, any container tag such as `title`, `artist` or `comment`, and exiftool tag names for images (`Artist`, `XMP-dc:Title`, `GPSLatitude`).

```sh
hypr-encode meta set movie.mkv --in-place title="My Movie"
hypr-encode meta set movie.mkv --in-place --track s:0 language=rus forced=1 default=0
hypr-encode meta rm ./Photos/*.jpg --group GPS -o ./clean/
hypr-encode meta set edited.jpg --from original.jpg --in-place
```

Backends:

| File | Backend |
| --- | --- |
| Images (JPEG, PNG, WebP, TIFF, HEIC, AVIF, GIF, DNG and camera RAW) | `exiftool` |
| MP4, MOV, M4V, 3GP with group keys (`Keys:Make`), `--group` or `--from` | `exiftool` |
| Matroska (`mkv`, `mka`, `webm`) with `--in-place`, editing the container title or a track's `title`, `language`, `default`, `forced` | `mkvpropedit`, edits in place without rewriting the file |
| Everything else, including MP3 and FLAC | `ffmpeg` remux with `-c copy` |

ffmpeg and exiftool write a temp file next to the target and rename it only on success, so `--in-place` never leaves a half-written file and exiftool never leaves `*_original` backups. mkvpropedit edits the Matroska header in place by design. Batches use one exiftool process (`-stay_open`). A track that does not exist is an error. exiftool writes fail when a tag is unknown, not writable, or has an invalid value.

### doctor

```text
hypr-encode doctor [--json]
```

Checks `ffprobe`, `ffmpeg`, `mkvpropedit`, `exiftool` and `dcraw_emu`: whether each is in `PATH` and which version it reports (for `dcraw_emu`, the LibRaw version printed by `simple_dcraw` next to it). It also test-encodes a short clip with every GPU preset, since an encoder listed by ffmpeg may still lack the hardware or driver. Exits with code 4 if any tool is missing; GPU results do not affect the exit code. Commands that do not need a missing tool keep working.

### tui

```text
hypr-encode tui [dir]
```

Opens a full-screen interface in `dir` (default: the current directory): a file browser on the left and the Info, Tracks, Metadata and Convert tabs on the right. It starts only with this command and needs stdin and stdout to be a terminal; it exits with code 2 otherwise. `--json` is not supported.

| Key | Action |
| --- | --- |
| `1`–`4` | Switch tabs |
| `tab` | Move focus between the file browser and the tab |
| `↑`/`↓` or `k`/`j` | Move the cursor |
| `enter`, `→` / `⌫`, `←` | Open a directory / go to the parent (browser) |
| `space` | Mark a file for conversion (browser) |
| `q`, `ctrl+c` | Quit; a running queue is stopped first and its temp files removed |

**Metadata** lists container tags (or image tags by group) and every track with its tags. `enter` edits a value, `a` adds `KEY=VALUE` to the selected container or track, `d` deletes a tag, `t` and `f` toggle a track's default and forced flags, `u` drops all pending changes. Changes stay pending until `w`, which shows them with the exact commands; then `y` writes in place, `c` writes a `name.edited.ext` copy (an existing copy is never overwritten) and `esc` goes back. Moving to another file drops pending changes.

**Convert** picks a preset with `←`/`→` (only presets usable on this machine) and overrides its fields with `enter`; an empty field keeps the preset value. `r` shows the output names for the marked files, or the selected one, in the output directory (default `converted/` under the starting directory), flagging outputs that exist; `enter` starts the same queue as `convert`, `esc` goes back. `o` switches what happens to existing outputs (`fail`, `overwrite`, `increment`; the default comes from `config.yml`) and updates the names. Each job shows a progress bar, plus a total; `pgup`/`pgdn` scroll the list, which follows the running job. `+` and `-` change the number of parallel jobs, `0` returns to automatic limits; a new count applies to jobs started afterwards. `x` stops the queue. `s` saves the current preset and overrides as a user preset that extends it.

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

`meta get --json` prints `{"schemaVersion":1,"files":[{"path","tags","tracks":[{"index","selector","type","codec","default","forced","tags"}],"groups":{...}}],"errors":[...]}`; `groups` appears only when exiftool read the file. `meta set|rm --json` prints `{"schemaVersion":1,"results":[{"path","output","backend","command"}],"errors":[...]}`, where `command` is set with `--dry-run`. See [`testdata/meta-get.golden`](testdata/meta-get.golden) and [`testdata/meta-edit.golden`](testdata/meta-edit.golden).

In `info --json`, image files carry an `exif` object with the same groups, and every track has `default` and `tags`.

`convert --dry-run --json` prints `{"schemaVersion":1,"jobs":[{"jobId","input","output","preset","load","threads","decoder":[...],"command":["ffmpeg",...]}]}`, where `decoder` is the `dcraw_emu` command whose output the ffmpeg command reads, set only for camera RAW; see [`testdata/convert-dry-run.golden`](testdata/convert-dry-run.golden). The command shows the final output path; the real run writes to a temp file first.

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
