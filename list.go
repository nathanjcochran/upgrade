package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

func list(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "go", "list", "-mod=mod", "./...")

	if err := cmd.Run(); err != nil {
		if err := err.(*exec.ExitError); err != nil {
			fmt.Println(string(err.Stderr)) // TODO: Remove
		}
		return fmt.Errorf("error executing 'go list' command: %s", err)
	}
	return nil
}

// From "go help list" output
type Module struct {
	Path       string       // module path
	Query      string       // version query corresponding to this version
	Version    string       // module version
	Versions   []string     // available module versions
	Replace    *Module      // replaced by this module
	Time       *time.Time   // time version was created
	Update     *Module      // available update (with -u)
	Main       bool         // is this the main module?
	Indirect   bool         // module is only indirectly needed by main module
	Dir        string       // directory holding local copy of files, if any
	GoMod      string       // path to go.mod file describing module, if any
	GoVersion  string       // go version used in module
	Retracted  []string     // retraction information, if any (with -retracted or -u)
	Deprecated string       // deprecation message, if any (with -u)
	Error      *ModuleError // error loading module
	Origin     any          // provenance of module
	Reuse      bool         // reuse of old module info is safe
}

type ModuleError struct {
	Err string // the error itself
}

func listModules(ctx context.Context, modulePaths ...string) ([]Module, error) {
	cmd := exec.CommandContext(ctx,
		"go", append([]string{"list", "-m", "-u", "-e", "-json", "-mod=readonly"},
			modulePaths...,
		)...,
	)
	out, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			fmt.Println(string(err.Stderr)) // TODO: Remove
		}
		return nil, fmt.Errorf("error executing 'go list -m -u -e -json -mod=readonly' command: %s", err)
	}

	var results []Module
	decoder := json.NewDecoder(bytes.NewReader(out))
	for decoder.More() {
		var result Module
		if err := decoder.Decode(&result); err != nil {
			return nil, fmt.Errorf("error parsing results of 'go list -m -u -e -json -mod=readonly' command: %s", err)
		}
		results = append(results, result)
	}
	return results, nil
}
