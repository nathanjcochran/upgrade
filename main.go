package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/printer"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
)

var (
	filePath = flag.String("f", "./go.mod", "go.mod file path") // TODO: Just take path to module root
	verbose  = flag.Bool("v", false, "verbose output")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [-f modfile path] [-v] module [version]:\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "Upgrades the named module dependency to the specified version,\n")
		fmt.Fprintf(flag.CommandLine.Output(), "or, if no version is given, to the highest major version available.\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "The module should be given as a fully qualified module path\n")
		fmt.Fprintf(flag.CommandLine.Output(), "(including the major version component, if applicable).\n")
		fmt.Fprintf(flag.CommandLine.Output(), "For example: github.com/nathanjcochran/gomod.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	path := flag.Arg(0)
	if path == "" {
		flag.Usage()
		os.Exit(2)
	}

	// If the module path is "all", upgrade all
	// modules with an available higher version.
	if path == "all" {
		upgradeAll()
		return
	}

	// Validate and parse the module path
	if err := module.CheckPath(path); err != nil {
		log.Fatalf("Invalid module path %s: %s", path, err)
	}

	prefix, currentMajor, ok := module.SplitPathVersion(path)
	if !ok {
		log.Fatalf("Invalid module path: %s", path)
	}

	targetVersion := flag.Arg(1)
	if targetVersion == "" {
		// If no target major version was given, call 'go list -m'
		// to find the highest available major version
		targetVersion, err := getTargetVersion(prefix, currentMajor)
		if err != nil {
			log.Fatalf("Error finding target version: %s", err)
		}
		if targetVersion == "" {
			log.Fatalf("No versions available for upgrade")
		}
		if *verbose {
			fmt.Printf("Found target version: %s/%s\n", prefix, targetVersion)
		}
	} else if !semver.IsValid(targetVersion) {
		log.Fatalf("Invalid target version: %s", targetVersion)
	}
	targetMajor := semver.Major(targetVersion)

	// Figure out what the post-upgrade module path should be
	var newPath string
	switch targetMajor {
	case "v0", "v1":
		newPath = prefix
	default:
		newPath = fmt.Sprintf("%s/%s", prefix, targetMajor)
	}

	// Read and parse the go.mod file
	b, err := ioutil.ReadFile(*filePath)
	if err != nil {
		log.Fatalf("Error reading module file %s: %s", *filePath, err)
	}

	file, err := modfile.Parse(*filePath, b, nil)
	if err != nil {
		log.Fatalf("Error parsing module file %s: %s", *filePath, err)
	}

	//out, err := json.MarshalIndent(file, "", "  ")
	//if err != nil {
	//	log.Fatalf("Error marshaling module file to JSON: %s", err)
	//}
	//fmt.Printf("%s\n", string(out))

	// Make sure the given module is actually a dependency in the go.mod file
	found := false
	for _, require := range file.Require {
		if require.Mod.Path == path {
			found = true
			break
		}
	}

	if !found {
		log.Fatalf("Module not a known dependency: %s", path)
	}

	// Rewrite import paths in files
	rewriteImports(path, newPath)

	// Get the full version for the upgraded dependency
	// (with the highest available minor/patch version)
	// TODO: Only if targetVersion is not already fully qualified
	version, err := getFullVersion(newPath, targetVersion)
	if err != nil {
		log.Fatalf("Error getting full upgrade version: %s", err)
	}

	// Drop the old module dependency and add the new, upgraded one
	if err := file.DropRequire(path); err != nil {
		log.Fatalf("Error dropping module requirement %s: %s", path, err)
	}
	if err := file.AddRequire(newPath, version); err != nil {
		log.Fatalf("Error adding module requirement %s: %s", newPath, err)
	}

	// Format and re-write the module file
	file.SortBlocks()
	file.Cleanup()
	out, err := file.Format()
	if err != nil {
		log.Fatalf("Error formatting module file: %s", err)
	}

	if err := ioutil.WriteFile(*filePath, out, 0644); err != nil {
		log.Fatalf("Error writing module file %s: %s", *filePath, err)
	}
}

func upgradeAll() {
	// Read and parse the go.mod file
	b, err := ioutil.ReadFile(*filePath)
	if err != nil {
		log.Fatalf("Error reading module file %s: %s", *filePath, err)
	}

	file, err := modfile.Parse(*filePath, b, nil)
	if err != nil {
		log.Fatalf("Error parsing module file %s: %s", *filePath, err)
	}

	// For each requirement, check if there is a higher major version available
	for _, require := range file.Require {
		prefix, currentMajor, ok := module.SplitPathVersion(require.Mod.Path)
		if !ok {
			log.Fatalf("Invalid module path: %s", require.Mod.Path)
		}

		targetVersion, err := getTargetVersion(prefix, currentMajor)
		if err != nil {
			log.Fatalf("Error getting upgrade version for module %s: %s",
				require.Mod.Path, err,
			)
		}

		if targetVersion == "" {
			if *verbose {
				fmt.Printf("%s - no versions available for upgrade", require.Mod.Path)
			}
			continue
		}
		targetMajor := semver.Major(targetVersion)

		var newPath string
		switch targetMajor {
		case "v0", "v1":
			newPath = prefix
		default:
			newPath = fmt.Sprintf("%s/%s", prefix, targetMajor)
		}

		// Drop the old module dependency and add the new, upgraded one
		if err := file.DropRequire(require.Mod.Path); err != nil {
			log.Fatalf("Error dropping module requirement %s: %s",
				require.Mod.Path, err,
			)
		}
		if err := file.AddRequire(newPath, targetVersion); err != nil {
			log.Fatalf("Error adding module requirement %s: %s", newPath, err)
		}
	}

	// Format and re-write the module file
	file.SortBlocks()
	file.Cleanup()
	out, err := file.Format()
	if err != nil {
		log.Fatalf("Error formatting module file: %s", err)
	}

	if err := ioutil.WriteFile(*filePath, out, 0644); err != nil {
		log.Fatalf("Error writing module file %s: %s", *filePath, err)
	}
}

const batchSize = 25

func getTargetVersion(prefix, currentMajor string) (string, error) {
	// We're always upgrading, so start at v2
	version := 2

	// If the dependency already has a major version in its
	// import path, start there
	if currentMajor != "" {
		version, err := strconv.Atoi(currentMajor[1:])
		if err != nil {
			return "", fmt.Errorf("invalid major version '%s': %s", currentMajor, err)
		}
		version++
	}

	var targetVersion string
	for {
		// Make batched calls to 'go list -m' for
		// better performance (ideally, a single call).
		var batch []string
		for i := 0; i < batchSize; i++ {
			modulePath := fmt.Sprintf("%s/v%d@v%d", prefix, version, version)
			batch = append(batch, modulePath)
			version++
		}

		cmd := exec.CommandContext(context.Background(),
			"go", append([]string{"list", "-m", "-e", "-json"},
				batch...,
			)...,
		)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("error executing 'go list -m -e -json' command: %s", err)
		}

		decoder := json.NewDecoder(bytes.NewReader(out))
		for decoder.More() {
			var result struct {
				Version string
				Error   struct {
					Err string
				}
			}
			if err := decoder.Decode(&result); err != nil {
				return "", fmt.Errorf("error parsing results of 'go list -m -e -json' command: %s", err)
			}

			// TODO: Checking the content of the error message is hacky,
			// but it's the only way I could differentiate errors due to
			// incompatible pre-module versions from errors due to unavailable
			// (i.e. not yet released) versions.
			if result.Error.Err == "" {
				targetVersion = result.Version
			} else if strings.Contains(result.Error.Err, "no matching versions for query") {
				return targetVersion, nil
			} else if *verbose {
				fmt.Println(result.Error.Err)
			}
		}
	}
}

func getFullVersion(path, targetVersion string) (string, error) {
	cmd := exec.CommandContext(context.Background(),
		"go", "list", "-m", "-f", "{{.Version}}",
		fmt.Sprintf("%s@%s", path, targetVersion),
	)
	version, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			fmt.Println(string(err.Stderr))
		}
		return "", fmt.Errorf("error executing 'go list -m -f {{.Version}}' command: %s", err)
	}

	return strings.TrimSpace(string(version)), nil
}

func rewriteImports(oldPath, newPath string) {
	cfg := &packages.Config{Mode: packages.LoadSyntax}
	pkgs, err := packages.Load(cfg, "./...") // TODO: Take as arg
	if err != nil {
		log.Fatalf("Error loading package info: %s", err)
	}

	if len(pkgs) < 1 {
		log.Fatalf("Failed to find/load package info")
	}

	for _, pkg := range pkgs {
		if *verbose {
			fmt.Println(pkg.Name)
		}
		for i, fileAST := range pkg.Syntax {
			filename := pkg.CompiledGoFiles[i]

			var found bool
			for _, fileImp := range fileAST.Imports {
				importPath := strings.Trim(fileImp.Path.Value, "\"")
				if strings.HasPrefix(importPath, oldPath) {
					found = true
					newImportPath := strings.Replace(importPath, oldPath, newPath, 1)
					if *verbose {
						fmt.Printf("%s:\n\t%s\n\t-> %s\n", filename, importPath, newImportPath)
					}
					fileImp.Path.Value = fmt.Sprintf("\"%s\"", newImportPath)
				}
			}
			if found {
				f, err := os.Create(filename)
				if err != nil {
					f.Close()
					log.Fatalf("Error opening file %s: %s", filename, err)
				}
				if err := printer.Fprint(f, pkg.Fset, fileAST); err != nil {
					f.Close()
					log.Fatalf("Error writing to file %s: %s", filename, err)
				}
				f.Close()
			}
		}
	}
}
