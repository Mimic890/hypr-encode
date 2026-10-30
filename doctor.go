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
}

// doctor reports every external tool; it exits 4 if any of them is missing.
func (a *app) doctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	args, err := a.parseFlags(fs, args, "doctor [--json]")
	if err != nil {
		return err
	}
	if len(args) > 0 {
		return usageErr("doctor: unexpected argument %q", args[0])
	}

	doc := doctorDoc{SchemaVersion: schemaVersion}
	var missing []string
	for _, name := range tools.Known {
		st := tools.Check(ctx, name)
		doc.Tools = append(doc.Tools, st)
		if !st.Found {
			missing = append(missing, name)
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
		printDoctor(a.textOut(), doc.Tools)
	}
	if len(missing) > 0 {
		return &codedError{exitMissingTool, fmt.Errorf("missing tools: %s", strings.Join(missing, ", "))}
	}
	return nil
}
