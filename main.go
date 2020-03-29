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
	"path"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/tools/go/packages"
)

const usage = `Usage: %s [-d directory] [-v] [module [version]]

Upgrades the named module dependency to the specified version, or, if no
version is given, to the highest major/minor/patch version available. Updates
the dependency's require directive in the go.mod file, as well as the relevant
import paths in all Go files in the current module (the current package and
all subpackages).

The module should be given as a fully qualified module path, as it is written
in the go.mod file (including the major version component, if applicable).

For example: github.com/nathanjcochran/upgrade

The version must be a valid semver module version. It can be provided with any
level of major/minor/patch specificity - e.g. 'v2', 'v2.3', 'v.2.3.4'. The
tool will always attempt to upgrade to the highest available matching version.

If no module is given, or if the special target "all" is given, the tool
will attempt to upgrade all direct dependencies in the go.mod file to the
highest major version available.

Note that updating major versions of dependencies is likely to introduce
compilation and/or runtime errors due to backwards-incompatibile changes. You
should be prepared to address errors after the upgrade process completes.

By default, the tool assumes the module to update is rooted in the current
directory. The [-d directory] flag can be provided to override that behavior.

The [-v] flag turns on verbose output.

Options:
`

var (
	dir     = flag.String("d", ".", "Module directory path")
	verbose = flag.Bool("v", false, "verbose output")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), usage, os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	file := readModFile(*dir)

	path := flag.Arg(0)
	version := flag.Arg(1)
	switch path {
	case "", "all":
		upgradeAll(file)
	default:
		upgradeOne(file, path, version)
	}

	writeModFile(*dir, file)
}

func readModFile(dir string) *modfile.File {
	// Read and parse the go.mod file
	filePath := path.Join(dir, "go.mod")
	b, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Error reading module file %s: %s", filePath, err)
	}

	file, err := modfile.Parse(filePath, b, nil)
	if err != nil {
		log.Fatalf("Error parsing module file %s: %s", filePath, err)
	}

	return file
}

func writeModFile(dir string, f *modfile.File) {
	// Format and re-write the module file
	f.SortBlocks()
	f.Cleanup()
	out, err := f.Format()
	if err != nil {
		log.Fatalf("Error formatting module file: %s", err)
	}

	filePath := path.Join(dir, "go.mod")
	if err := ioutil.WriteFile(filePath, out, 0644); err != nil {
		log.Fatalf("Error writing module file %s: %s", filePath, err)
	}
}

func upgradeOne(file *modfile.File, path, version string) {
	// Validate and parse the module path
	if err := module.CheckPath(path); err != nil {
		log.Fatalf("Invalid module path %s: %s", path, err)
	}

	switch version {
	case "":
		// If no target major version was given, call 'go list -m'
		// to find the highest available major version
		var err error
		version, err = GetUpgradeVersion(path)
		if err != nil {
			log.Fatalf("Error finding upgrade version: %s", err)
		}
		if version == "" {
			log.Fatalf("No versions available for upgrade")
		}
	default:
		if !semver.IsValid(version) {
			log.Fatalf("Invalid upgrade version: %s", version)
		}
	}

	// Figure out what the post-upgrade module path should be
	newPath, err := UpgradePath(path, version)
	if err != nil {
		log.Fatalf("Error upgrading module path %s to %s: %s",
			path, version, err,
		)
	}

	// Get the full version for the upgraded dependency
	// (with the highest available minor/patch version)
	if module.CanonicalVersion(version) != version {
		version, err = GetFullVersion(newPath, version)
		if err != nil {
			log.Fatalf("Error getting full upgrade version: %s", err)
		}
	}

	//out, err := json.MarshalIndent(file, "", "  ")
	//if err != nil {
	//	log.Fatalf("Error marshaling module file to JSON: %s", err)
	//}
	//fmt.Printf("%s\n", string(out))

	// Make sure the given module is actually a dependency in the go.mod file
	var (
		found      = false
		oldVersion = ""
	)
	for _, require := range file.Require {
		if require.Mod.Path == path {
			found = true
			oldVersion = require.Mod.Version
			break
		}
	}

	if !found {
		log.Fatalf("Module not a known dependency: %s", path)
	}

	fmt.Printf("%s %s -> %s %s\n", path, oldVersion, newPath, version)

	// Drop the old module dependency and add the new, upgraded one
	if err := file.DropRequire(path); err != nil {
		log.Fatalf("Error dropping module requirement %s: %s", path, err)
	}
	if err := file.AddRequire(newPath, version); err != nil {
		log.Fatalf("Error adding module requirement %s: %s", newPath, err)
	}

	// Rewrite import paths in files
	rewriteImports([]Upgrade{{OldPath: path, NewPath: newPath}})
}

func upgradeAll(file *modfile.File) {
	// For each requirement, check if there is a higher major version available
	var upgrades []Upgrade
	for _, require := range file.Require {
		if require.Indirect {
			continue
		}

		version, err := GetUpgradeVersion(require.Mod.Path)
		if err != nil {
			log.Fatalf("Error getting upgrade version for module %s: %s",
				require.Mod.Path, err,
			)
		}

		if version == "" {
			if *verbose {
				fmt.Printf("%s - no versions available for upgrade\n", require.Mod.Path)
			}
			continue
		}

		newPath, err := UpgradePath(require.Mod.Path, version)
		if err != nil {
			log.Fatalf("Error upgrading module path %s to %s: %s",
				require.Mod.Path, version, err,
			)
		}

		upgrades = append(upgrades, Upgrade{
			OldPath: require.Mod.Path,
			NewPath: newPath,
		})

		fmt.Printf("%s %s -> %s %s\n", require.Mod.Path, require.Mod.Version, newPath, version)

		// Drop the old module dependency and add the new, upgraded one
		if err := file.DropRequire(require.Mod.Path); err != nil {
			log.Fatalf("Error dropping module requirement %s: %s",
				require.Mod.Path, err,
			)
		}
		if err := file.AddRequire(newPath, version); err != nil {
			log.Fatalf("Error adding module requirement %s: %s", newPath, err)
		}
	}

	rewriteImports(upgrades)
}

func UpgradePath(path, version string) (string, error) {
	prefix, _, ok := module.SplitPathVersion(path)
	if !ok {
		return "", fmt.Errorf("invalid module path: %s", path)
	}

	major := semver.Major(version)
	switch major {
	case "v0", "v1":
		return prefix, nil
	}
	return fmt.Sprintf("%s/%s", prefix, major), nil
}

const batchSize = 5

func GetUpgradeVersion(path string) (string, error) {
	// Split module path
	prefix, pathMajor, ok := module.SplitPathVersion(path)
	if !ok {
		return "", fmt.Errorf("invalid module path: %s", path)
	}

	// If the dependency already has a major version in its import
	// path, start there. Otherwise, start at v2 (since we're always
	// upgrading to at least v2)
	version := 2
	if pathMajor != "" {
		var err error
		version, err = strconv.Atoi(strings.TrimPrefix(pathMajor, "/v"))
		if err != nil {
			return "", fmt.Errorf(
				"invalid major version '%s': %s", pathMajor, err,
			)
		}
		version++
	}

	var upgradeVersion string
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
			if err := err.(*exec.ExitError); err != nil {
				fmt.Println(string(err.Stderr))
			}
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
				upgradeVersion = result.Version
			} else if strings.Contains(result.Error.Err, "no matching versions for query") {
				return upgradeVersion, nil
			} else if *verbose {
				fmt.Println(result.Error.Err)
			}
		}
	}
}

func GetFullVersion(path, version string) (string, error) {
	cmd := exec.CommandContext(context.Background(),
		"go", "list", "-m", "-f", "{{.Version}}",
		fmt.Sprintf("%s@%s", path, version),
	)
	fullVersion, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			fmt.Println(string(err.Stderr))
		}
		return "", fmt.Errorf("error executing 'go list -m -f {{.Version}}' command: %s", err)
	}

	return strings.TrimSpace(string(fullVersion)), nil
}

type Upgrade struct {
	OldPath string
	NewPath string
}

func rewriteImports(upgrades []Upgrade) {
	cfg := &packages.Config{Mode: packages.LoadSyntax}
	loadPath := fmt.Sprintf("%s/...", path.Clean(*dir))
	pkgs, err := packages.Load(cfg, loadPath)
	if err != nil {
		log.Fatalf("Error loading package info: %s", err)
	}

	if len(pkgs) < 1 {
		log.Fatalf("Failed to find/load package info")
	}

	for _, pkg := range pkgs {
		if *verbose {
			fmt.Printf("Package: %s\n", pkg.Name)
		}
		for i, fileAST := range pkg.Syntax {
			filename := pkg.CompiledGoFiles[i]

			var found bool
			for _, fileImp := range fileAST.Imports {
				importPath := strings.Trim(fileImp.Path.Value, "\"")

				for _, upgrade := range upgrades {
					if strings.HasPrefix(importPath, upgrade.OldPath) {
						if !found {
							found = true
							if *verbose {
								fmt.Printf("%s:\n", filename)
							}
						}
						newImportPath := strings.Replace(importPath, upgrade.OldPath, upgrade.NewPath, 1)
						fileImp.Path.Value = fmt.Sprintf("\"%s\"", newImportPath)
						if *verbose {
							fmt.Printf("\t%s -> %s\n", importPath, newImportPath)
						}
					}
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
