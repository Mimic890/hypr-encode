package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/Mimic890/hypr-encode/pkg/tools"
)

type doctorDoc struct {
	SchemaVersion int            `json:"schemaVersion"`
	Tools         []tools.Status `json:"tools"`
	GPUPresets    []gpuStatus    `json:"gpuPresets"`
}

type gpuStatus struct {
	Name    string `json:"name"`
	Encoder string `json:"encoder"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// doctor reports every external tool and test-encodes every GPU preset.
// It exits 4 if any tool is missing; GPU results do not affect the exit code.
func (a *app) doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	args, err := a.parseFlags(fs, args, "doctor [--json]")
	if err != nil {
		return err
	}
	if len(args) > 0 {
		return usageErr("doctor: unexpected argument %q", args[0])
	}

	doc := doctorDoc{SchemaVersion: schemaVersion, GPUPresets: []gpuStatus{}}
	var missing []string
	for _, name := range tools.Known {
		st := tools.Check(ctx, name)
		doc.Tools = append(doc.Tools, st)
		if !st.Found {
			missing = append(missing, name)
		}
	}
	if doc.Tools[1].Found { // ffmpeg
		lib, err := loadLibrary()
		if err != nil {
			return err
		}
		_, gpu, err := lib.Available(ctx)
		if err != nil {
			return err
		}
		for _, name := range lib.Names() {
			if err, ok := gpu[name]; ok {
				p, _ := lib.Get(name)
				st := gpuStatus{Name: name, Encoder: p.Video.Codec, OK: err == nil}
				if err != nil {
					st.Error = err.Error()
				}
				doc.GPUPresets = append(doc.GPUPresets, st)
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if a.json {
		if err := a.writeJSON(doc); err != nil {
			return err
		}
	} else {
		printDoctor(a.textOut(), doc)
	}
	if len(missing) > 0 {
		return &codedError{exitMissingTool, fmt.Errorf("missing tools: %s", strings.Join(missing, ", "))}
	}
	return nil
}
