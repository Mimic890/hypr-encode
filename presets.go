package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"go.yaml.in/yaml/v3"

	"github.com/Mimic890/hypr-encode/pkg/convert"
)

const presetUsage = `Usage: hypr-encode preset <command> [flags]

Commands:
  list                       List presets usable on this machine
  show <name>                Show a preset with its extends chain resolved
  save <name> [flags]        Save convert flags as a user preset (--preset sets the base)
  rm <name> --yes            Remove a user preset
  edit                       Open presets.yml in $VISUAL or $EDITOR
`

func (a *app) preset(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageErr("preset: missing command\n\n%s", presetUsage)
	}
	switch args[0] {
	case "list":
		return a.presetList(ctx, args[1:])
	case "show":
		return a.presetShow(args[1:])
	case "save":
		return a.presetSave(args[1:])
	case "rm":
		return a.presetRemove(args[1:])
	case "edit":
		return a.presetEdit(ctx, args[1:])
	case "help", "-h", "-help", "--help":
		fmt.Fprint(a.stdout, presetUsage)
		return nil
	}
	return usageErr("preset: unknown command %q\n\n%s", args[0], presetUsage)
}

type unavailablePreset struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

func (a *app) presetList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("preset list", flag.ContinueOnError)
	if args, err := a.parseFlags(fs, args, "preset list [--json]"); err != nil || len(args) > 0 {
		return cmpErr(err, usageErr("preset list: unexpected argument"))
	}
	lib, err := loadLibrary()
	if err != nil {
		return err
	}
	presets, gpu, err := lib.Available(ctx)
	if err != nil {
		return err
	}
	doc := struct {
		SchemaVersion int                 `json:"schemaVersion"`
		Presets       []convert.Preset    `json:"presets"`
		Unavailable   []unavailablePreset `json:"unavailable"`
	}{schemaVersion, presets, []unavailablePreset{}}
	for _, name := range lib.Names() {
		if err := gpu[name]; err != nil {
			doc.Unavailable = append(doc.Unavailable, unavailablePreset{name, err.Error()})
		}
	}
	if a.json {
		return a.writeJSON(doc)
	}

	tw := tabwriter.NewWriter(a.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tMEDIA\tLOAD\tSOURCE\tDESCRIPTION")
	for _, p := range presets {
		source := p.Source
		if source == "user" && lib.IsBuiltin(p.Name) {
			source = "user (overrides builtin)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Media, p.Load, source, safeText(p.Description))
	}
	tw.Flush()
	if n := len(doc.Unavailable); n > 0 {
		fmt.Fprintf(a.stderr, "%d GPU presets failed a test encode on this machine and are hidden; see hypr-encode doctor\n", n)
	}
	return nil
}

func (a *app) presetShow(args []string) error {
	fs := flag.NewFlagSet("preset show", flag.ContinueOnError)
	args, err := a.parseFlags(fs, args, "preset show [--json] <name>")
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return usageErr("preset show: expected one preset name")
	}
	lib, err := loadLibrary()
	if err != nil {
		return err
	}
	p, err := lib.Get(args[0])
	if errors.Is(err, convert.ErrUnknownPreset) {
		return usageErr("preset show: %v", err)
	} else if err != nil {
		return err
	}
	if a.json {
		return a.writeJSON(struct {
			SchemaVersion int            `json:"schemaVersion"`
			Preset        convert.Preset `json:"preset"`
		}{schemaVersion, p})
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "# source: %s\n%s", p.Source, data)
	return nil
}

func (a *app) presetSave(args []string) error {
	fs := flag.NewFlagSet("preset save", flag.ContinueOnError)
	base := fs.String("preset", "", "preset to extend")
	description := fs.String("description", "", "one-line description")
	media := fs.String("media", "", "media type: video, audio or image")
	yes := fs.Bool("yes", false, "replace an existing user preset with the same name")
	pf := addPresetFlags(fs)
	args, err := a.parseFlags(fs, args, "preset save [flags] <name>")
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return usageErr("preset save: expected one preset name")
	}
	lib, err := loadLibrary()
	if err != nil {
		return err
	}
	p := convert.Preset{Name: args[0], Extends: *base, Description: *description, Media: *media}
	mainMedia := p.Media // decides where --crf goes
	if mainMedia == "" && p.Extends != "" {
		parent, err := lib.Get(p.Extends)
		if err != nil {
			return usageErr("preset save: %v", err)
		}
		mainMedia = parent.Media
	}
	pf.apply(&p, mainMedia)
	if existing, err := lib.Get(p.Name); err == nil && existing.Source == "user" && !*yes {
		return usageErr("preset save: user preset %s exists; pass --yes to replace it", p.Name)
	}
	if err := lib.Save(p); err != nil {
		return usageErr("preset save: %v", err)
	}
	if !a.json {
		fmt.Fprintf(a.stderr, "saved preset %s\n", p.Name)
	}
	return nil
}

func (a *app) presetRemove(args []string) error {
	fs := flag.NewFlagSet("preset rm", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "confirm the removal")
	args, err := a.parseFlags(fs, args, "preset rm --yes <name>")
	if err != nil {
		return err
	}
	if len(args) != 1 {
		return usageErr("preset rm: expected one preset name")
	}
	if !*yes {
		return usageErr("preset rm: pass --yes to remove preset %s", args[0])
	}
	lib, err := loadLibrary()
	if err != nil {
		return err
	}
	if err := lib.Remove(args[0]); err != nil {
		return usageErr("preset rm: %v", err)
	}
	return nil
}

const presetTemplate = `# hypr-encode user presets. A preset with a built-in's name overrides it.
# Fields: name, description, extends, media (video|audio|image), container,
# load (cpu-heavy|light|gpu|io), video/audio/subtitle {codec, crf, bitrate,
# preset, pixFmt, args}, tracks, inputArgs, args.
#
# - name: my-hevc
#   extends: hevc
#   video: {crf: 20}
`

func (a *app) presetEdit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("preset edit", flag.ContinueOnError)
	if args, err := a.parseFlags(fs, args, "preset edit"); err != nil || len(args) > 0 {
		return cmpErr(err, usageErr("preset edit: unexpected argument"))
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		return usageErr("preset edit: set $VISUAL or $EDITOR")
	}
	path, err := configPath("presets.yml")
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(presetTemplate), 0o644); err != nil {
			return err
		}
	}
	// The editor command may carry flags ("code -w"); it is split on spaces, never run through a shell.
	parts := strings.Fields(editor)
	cmd := exec.CommandContext(ctx, parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor: %w", err)
	}
	if _, err := convert.LoadLibrary(path); err != nil {
		return fmt.Errorf("presets.yml is invalid, run preset edit again: %w", err)
	}
	return nil
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
