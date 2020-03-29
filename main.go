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

var (
	dir     = flag.String("d", ".", "Module directory path")
	verbose = flag.Bool("v", false, "verbose output")
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [-d module directory] [-v] module [version]:\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "Upgrades the named module dependency to the specified version,\n")
		fmt.Fprintf(flag.CommandLine.Output(), "or, if no version is given, to the highest major version available.\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "The module should be given as a fully qualified module path\n")
		fmt.Fprintf(flag.CommandLine.Output(), "(including the major version component, if applicable).\n")
		fmt.Fprintf(flag.CommandLine.Output(), "For example: github.com/nathanjcochran/gomod.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	modulePath := flag.Arg(0)
	switch modulePath {
	case "":
		flag.Usage()
		os.Exit(2)
	case "all":
		upgradeAll()
	default:
		upgradeOne(modulePath, flag.Arg(1))
	}
}

func upgradeOne(modulePath, upgradeVersion string) {
	// Validate and parse the module path
	if err := module.CheckPath(modulePath); err != nil {
		log.Fatalf("Invalid module path %s: %s", modulePath, err)
	}

	switch upgradeVersion {
	case "":
		// If no target major version was given, call 'go list -m'
		// to find the highest available major version
		upgradeVersion, err := GetUpgradeVersion(modulePath)
		if err != nil {
			log.Fatalf("Error finding target version: %s", err)
		}
		if upgradeVersion == "" {
			log.Fatalf("No versions available for upgrade")
		}
		if *verbose {
			fmt.Printf("Found target version: %s\n", upgradeVersion)
		}
	default:
		if !semver.IsValid(upgradeVersion) {
			log.Fatalf("Invalid target version: %s", upgradeVersion)
		}
	}

	// Figure out what the post-upgrade module path should be
	newPath, err := UpgradePath(modulePath, upgradeVersion)
	if err != nil {
		log.Fatalf("Error upgrading module path %s to %s: %s",
			modulePath, upgradeVersion, err,
		)
	}

	// Get the full version for the upgraded dependency
	// (with the highest available minor/patch version)
	if module.CanonicalVersion(upgradeVersion) != upgradeVersion {
		upgradeVersion, err = GetFullVersion(newPath, upgradeVersion)
		if err != nil {
			log.Fatalf("Error getting full upgrade version: %s", err)
		}
	}

	// Read and parse the go.mod file
	filePath := path.Join(*dir, "go.mod")
	b, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Error reading module file %s: %s", filePath, err)
	}

	file, err := modfile.Parse(filePath, b, nil)
	if err != nil {
		log.Fatalf("Error parsing module file %s: %s", filePath, err)
	}

	//out, err := json.MarshalIndent(file, "", "  ")
	//if err != nil {
	//	log.Fatalf("Error marshaling module file to JSON: %s", err)
	//}
	//fmt.Printf("%s\n", string(out))

	// Make sure the given module is actually a dependency in the go.mod file
	found := false
	for _, require := range file.Require {
		if require.Mod.Path == modulePath {
			found = true
			break
		}
	}

	if !found {
		log.Fatalf("Module not a known dependency: %s", modulePath)
	}

	// Rewrite import paths in files
	rewriteImports(modulePath, newPath)

	// Drop the old module dependency and add the new, upgraded one
	if err := file.DropRequire(modulePath); err != nil {
		log.Fatalf("Error dropping module requirement %s: %s", modulePath, err)
	}
	if err := file.AddRequire(newPath, upgradeVersion); err != nil {
		log.Fatalf("Error adding module requirement %s: %s", newPath, err)
	}

	// Format and re-write the module file
	file.SortBlocks()
	file.Cleanup()
	out, err := file.Format()
	if err != nil {
		log.Fatalf("Error formatting module file: %s", err)
	}

	if err := ioutil.WriteFile(filePath, out, 0644); err != nil {
		log.Fatalf("Error writing module file %s: %s", filePath, err)
	}

}

func upgradeAll() {
	// Read and parse the go.mod file
	filePath := path.Join(*dir, "go.mod")
	b, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Error reading module file %s: %s", filePath, err)
	}

	file, err := modfile.Parse(filePath, b, nil)
	if err != nil {
		log.Fatalf("Error parsing module file %s: %s", filePath, err)
	}

	// For each requirement, check if there is a higher major version available
	for _, require := range file.Require {
		upgradeVersion, err := GetUpgradeVersion(require.Mod.Path)
		if err != nil {
			log.Fatalf("Error getting upgrade version for module %s: %s",
				require.Mod.Path, err,
			)
		}

		if upgradeVersion == "" {
			if *verbose {
				fmt.Printf("%s - no versions available for upgrade", require.Mod.Path)
			}
			continue
		}

		newPath, err := UpgradePath(require.Mod.Path, upgradeVersion)
		if err != nil {
			log.Fatalf("Error upgrading module path %s to %s: %s",
				require.Mod.Path, upgradeVersion, err,
			)
		}

		// Drop the old module dependency and add the new, upgraded one
		if err := file.DropRequire(require.Mod.Path); err != nil {
			log.Fatalf("Error dropping module requirement %s: %s",
				require.Mod.Path, err,
			)
		}
		if err := file.AddRequire(newPath, upgradeVersion); err != nil {
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

	if err := ioutil.WriteFile(filePath, out, 0644); err != nil {
		log.Fatalf("Error writing module file %s: %s", filePath, err)
	}
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

const batchSize = 25

func GetUpgradeVersion(path string) (string, error) {

	// Split module path
	prefix, pathMajor, ok := module.SplitPathVersion(path)
	if !ok {
		return "", fmt.Errorf("invalid module path: %s", path)
	}

	// We're always upgrading, so start at v2
	version := 2

	// If the dependency already has a major version in its
	// import path, start there
	if pathMajor != "" {
		version, err := strconv.Atoi(strings.TrimPrefix(pathMajor, "v"))
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

func rewriteImports(oldPath, newPath string) {
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
