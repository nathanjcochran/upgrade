package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
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

const usage = `Usage: %s [-d dir] [-v] [module] [version]

Upgrades the major version of a module - or the major version of one of its
dependencies - by editing the module's go.mod file and the corresponding import
paths in its Go files.

If no arguments are given, upgrades the major version of the module rooted in
the current directory. Increments the major version component of its path in
the go.mod file (adding the version component if necessary), and in any import
statements between the module's packages.

The same behavior is triggered by supplying the module's own path for the
[module] argument. However, in that form, a target [version] can also be given,
making it possible to jump several major versions at once (or to downgrade
versions).

If the module path of a dependency is given instead, upgrades the dependency to
the specified version, or, if no version is given, to the highest major version
available. Updates the dependency's require directive in the go.mod file, as
well as the relevant import paths in the module's Go files.

If the special target "all" is given, the tool attempts to upgrade all direct
dependencies in the go.mod file to the highest major version available.

If given, [module] must be a fully qualified module path, as written in the
go.mod file. It must include the major version component, if applicable. For
example: "github.com/nathanjcochran/upgrade/v2".

If [version] is given, it must be a valid semver module version. It can be
provided with any level of major/minor/patch specificity - e.g. 'v2', 'v2.3',
'v.2.3.4'. When upgrading the current module, only the major component of the
provided version is taken into account. When upgrading a dependency, the tool
will attempt to upgrade to the highest available matching version, unless the
target major version of the dependency is already required, in which case it
will maintain the existing minor/patch version.

NOTE: This tool does not add version tags in any version control systems.

By default, the tool assumes the module being updated is rooted in the current
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
		if _, err := fmt.Fprintf(flag.CommandLine.Output(), usage, os.Args[0]); err != nil {
			log.Fatalf("Error outputting usage message: %s", err)
		}
		flag.PrintDefaults()
	}
	flag.Parse()

	file := readModFile(*dir)

	path := flag.Arg(0)
	version := flag.Arg(1)

	switch path {
	case "", file.Module.Mod.Path:
		upgradeModule(file, version)
	case "all":
		upgradeAllDependencies(file)
	default:
		upgradeDependency(file, path, version)
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

func upgradeModule(file *modfile.File, version string) {
	path := file.Module.Mod.Path

	if version != "" {
		if !semver.IsValid(version) {
			log.Fatalf("Invalid upgrade version: %s", version)
		}

		// Truncate the minor/patch versions
		version = semver.Major(version)
	}

	// Figure out what the post-upgrade module path should be
	// (if version is empty, simply increment the version number)
	newPath, err := upgradePath(path, version)
	if err != nil {
		log.Fatalf("Error upgrading module path %s to %s: %s",
			path, version, err,
		)
	}

	fmt.Printf("%s -> %s\n", path, newPath)

	if err := file.AddModuleStmt(newPath); err != nil {
		log.Fatalf("Error upgrading module to %s: %s", newPath, err)
	}

	// Rewrite import paths in files
	if err := rewriteImports(*dir, []upgrade{{oldPath: path, newPath: newPath}}); err != nil {
		log.Fatalf("Error rewriting imports: %s", err)
	}
}

func upgradeDependency(file *modfile.File, path, version string) {
	// Validate and parse the module path
	if err := module.CheckPath(path); err != nil {
		log.Fatalf("Invalid module path %s: %s", path, err)
	}

	var (
		newPath     string
		fullVersion string
	)
	switch version {
	case "":
		// If no target major version was given, call 'go list -m'
		// to find the highest available major version
		var err error
		fullVersion, err = getUpgradeVersion(path)
		if err != nil {
			log.Fatalf("Error finding upgrade version: %s", err)
		}
		if fullVersion == "" {
			log.Fatalf("No versions available for upgrade")
		}

		// Figure out what the post-upgrade module path should be
		newPath, err = upgradePath(path, fullVersion)
		if err != nil {
			log.Fatalf("Error upgrading module path %s to %s: %s", path, fullVersion, err)
		}
	default:
		// If a target version was given, make sure it's valid, then call
		// 'go list -m' to get the full version and path (which depends on
		// whether the version is incompatible or not)
		if !semver.IsValid(version) {
			log.Fatalf("Invalid upgrade version: %s", version)
		}

		var err error
		newPath, fullVersion, err = getUpgradePathAndFullVersion(path, version)
		if err != nil {
			log.Fatalf("Error getting upgrade path and version: %s", err)
		}
	}

	// Make sure the given module is actually a dependency in the go.mod file
	var (
		found             = false
		oldVersion        = ""
		alreadyExists     = false
		removePreexisting = false
	)
	for _, require := range file.Require {
		switch require.Mod.Path {
		case path:
			found = true
			oldVersion = require.Mod.Version
		case newPath:
			if strings.HasPrefix(require.Mod.Version, version) {
				// Only keep existing version if it matches
				// the provided version (and/or is more specific)
				alreadyExists = true
				fullVersion = require.Mod.Version
			} else {
				// Otherwise, remove and replace the pre-existing dependency
				removePreexisting = true
			}
		}
	}

	if !found {
		log.Fatalf("Module not a known dependency: %s", path)
	}

	fmt.Printf("%s %s -> %s %s\n", path, oldVersion, newPath, fullVersion)

	// Drop the old module dependency and add the new, upgraded one (unless the
	// new major version of the dependency already existed as a dependency, in
	// which case, we drop it if didn't match the provided version, or maintain
	// it if it did)
	if err := file.DropRequire(path); err != nil {
		log.Fatalf("Error dropping module requirement %s: %s", path, err)
	}
	if removePreexisting {
		if err := file.DropRequire(newPath); err != nil {
			log.Fatalf("Error dropping module requirement %s: %s", newPath, err)
		}
	}
	if !alreadyExists {
		if err := file.AddRequire(newPath, fullVersion); err != nil {
			log.Fatalf("Error adding module requirement %s: %s", newPath, err)
		}
	}

	// If new path differs from old, rewrite import paths (paths can be the
	// same in case of minor version update)
	if newPath != path {
		// Rewrite import paths in files
		if err := rewriteImports(*dir, []upgrade{{oldPath: path, newPath: newPath}}); err != nil {
			log.Fatalf("Error rewriting imports: %s", err)
		}
	}
}

// TODO: Make concurrent
func upgradeAllDependencies(file *modfile.File) {
	// For each requirement, check if there is a higher major version available
	var upgrades []upgrade
	for _, require := range file.Require {
		if require.Indirect {
			continue
		}

		version, err := getUpgradeVersion(require.Mod.Path)
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

		newPath, err := upgradePath(require.Mod.Path, version)
		if err != nil {
			log.Fatalf("Error upgrading module path %s to %s: %s",
				require.Mod.Path, version, err,
			)
		}

		upgrades = append(upgrades, upgrade{
			oldPath: require.Mod.Path,
			newPath: newPath,
		})

		fmt.Printf("%s %s -> %s %s\n", require.Mod.Path, require.Mod.Version, newPath, version)

		// Drop the old module dependency and add the new, upgraded one
		// TODO: Don't add the new one if the same major version already
		// exists as a dependency
		if err := file.DropRequire(require.Mod.Path); err != nil {
			log.Fatalf("Error dropping module requirement %s: %s",
				require.Mod.Path, err,
			)
		}
		if err := file.AddRequire(newPath, version); err != nil {
			log.Fatalf("Error adding module requirement %s: %s", newPath, err)
		}
	}

	if err := rewriteImports(*dir, upgrades); err != nil {
		log.Fatalf("Error rewriting imports: %s", err)
	}
}

func upgradePath(path, version string) (string, error) {
	prefix, pathMajor, ok := module.SplitPathVersion(path)
	if !ok {
		return "", fmt.Errorf("invalid module path: %s", path)
	}

	if version == "" {
		// If no version was specified, upgrade to next sequential version
		if pathMajor == "" {
			version = "v2"
		} else {
			num, err := strconv.Atoi(strings.TrimPrefix(pathMajor, "v"))
			if err != nil {
				return "", fmt.Errorf("invalid major version in module path: %s", pathMajor)
			}
			num++
			version = fmt.Sprintf("v%d", num)
		}
	}

	major := semver.Major(version)
	switch major {
	case "v0", "v1":
		return prefix, nil
	}
	newPath := fmt.Sprintf("%s/%s", prefix, major)
	if err := module.CheckPath(newPath); err != nil {
		return "", fmt.Errorf("invalid module path after upgrade - %s: %s", newPath, err)

	}
	return newPath, nil
}

const batchSize = 5

func getUpgradeVersion(path string) (string, error) {
	// Split module path
	prefix, pathMajor, ok := module.SplitPathVersion(path)
	if !ok {
		return "", fmt.Errorf("invalid module path: %s", path)
	}

	var version int
	if pathMajor != "" {
		// If the dependency already has a major version in its import path,
		// start our search for a higher version there
		var err error
		version, err = strconv.Atoi(strings.TrimPrefix(pathMajor, "/v"))
		if err != nil {
			return "", fmt.Errorf("invalid major version '%s': %s", pathMajor, err)
		}
		version++
	} else {
		// Otherwise, get the highest available minor update version (including
		// incompatible versions, which allows us to skip over them and start
		// at the first valid major version)
		minorUpdateVersion, err := getMinorUpdateVersion(path)
		if err != nil {
			return "", fmt.Errorf("error getting minor update version for %s: %s", path, err)
		}

		major := semver.Major(minorUpdateVersion)
		version, err = strconv.Atoi(strings.TrimPrefix(major, "v"))
		if err != nil {
			return "", fmt.Errorf("invalid minor update version: %s", minorUpdateVersion)
		}
		version++

		// Make sure not to try upgrading path to /v1
		if version < 2 {
			version = 2
		}
	}

	// TODO: Consider actually upgrading to higher incompatible versions.
	// Would need to ensure that it's actually higher than the current version
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

			if result.Error.Err == "" {
				upgradeVersion = result.Version
			} else {
				if *verbose {
					fmt.Println(result.Error.Err)
				}
				return upgradeVersion, nil
			}
		}
	}
}

func getMinorUpdateVersion(path string) (string, error) {
	cmd := exec.CommandContext(context.Background(),
		"go", []string{"list", "-m", "-u", "-e", "-json",
			path,
		}...,
	)
	out, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			// TODO: Clean up random print statements
			fmt.Println(string(err.Stderr))
		}
		return "", fmt.Errorf("error executing 'go list -m -u -e -json' command: %s", err)
	}

	var result struct {
		Version string
		Update  struct {
			Version string
		}
		Error struct {
			Err string
		}
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("error parsing results of 'go list -m -u -e -json' command: %s", err)
	}

	if result.Error.Err != "" {
		return "", fmt.Errorf("error getting minor update version for %s: %s", path, result.Error.Err)
	}

	if result.Update.Version == "" {
		// Use current version if no update version is given (i.e. because
		// we're already at highest available minor version)
		if !semver.IsValid(result.Version) {
			return "", fmt.Errorf("invalid version returned from 'go list -m -u -e -json' command: %s", result.Version)
		}
		return result.Version, nil
	}

	if !semver.IsValid(result.Update.Version) {
		return "", fmt.Errorf("invalid update version returned from 'go list -m -u -e -json' command: %s", result.Update.Version)
	}
	return result.Update.Version, nil
}

func getUpgradePathAndFullVersion(path, version string) (string, string, error) {
	newPath, err := upgradePath(path, version)
	if err != nil {
		return "", "", fmt.Errorf("error upgrading module path %s to %s: %s", path, version, err)
	}

	cmd := exec.CommandContext(context.Background(),
		"go", "list", "-m", "-e", "-json",
		fmt.Sprintf("%s@%s", newPath, version),
	)
	out, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			// TODO: Clean up random print statements
			fmt.Println(string(err.Stderr))
		}
		return "", "", fmt.Errorf("error executing 'go list -m -e -json' command: %s", err)
	}

	var result struct {
		Version string
		Error   struct {
			Err string
		}
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", "", fmt.Errorf("error parsing results of 'go list -m -e -json' command: %s", err)
	}

	if result.Error.Err == "" {
		return newPath, result.Version, nil
	}

	// If the query did not return a result, make another query to check for an
	// incompatible version
	prefix, _, ok := module.SplitPathVersion(path)
	if !ok {
		return "", "", fmt.Errorf("invalid module path: %s", path)
	}

	cmd = exec.CommandContext(context.Background(),
		"go", "list", "-m", "-e", "-json",
		fmt.Sprintf("%s@%s", prefix, version),
	)
	out, err = cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			// TODO: Clean up random print statements
			fmt.Println(string(err.Stderr))
		}
		return "", "", fmt.Errorf("error executing 'go list -m -e -json' command: %s", err)
	}

	var result2 struct {
		Version string
		Error   struct {
			Err string
		}
	}
	if err := json.Unmarshal(out, &result2); err != nil {
		return "", "", fmt.Errorf("error parsing results of 'go list -m -e -json' command: %s", err)
	}

	if result2.Error.Err != "" {
		return "", "", fmt.Errorf("error getting upgrade path and version for %s: %s", path, result2.Error.Err)
	}

	return prefix, result2.Version, nil
}

func getModulePath(importPath string) (string, error) {
	cmd := exec.CommandContext(context.Background(),
		"go", "list", "-json", importPath,
	)
	out, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			fmt.Println(string(err.Stderr))
		}
		return "", fmt.Errorf("error executing 'go list -json' command: %s", err)
	}

	var result struct {
		Module struct {
			Path string
		}
		Standard bool
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("error parsing results of 'go list -json' command: %s", err)
	}

	// Standard library packages don't have a module
	if result.Standard {
		return importPath, nil
	}

	if result.Module.Path == "" {
		return "", fmt.Errorf("no module path returned from 'go list -json' command")
	}

	return result.Module.Path, nil
}

type upgrade struct {
	oldPath string
	newPath string
}

type file struct {
	name string
	ast  *ast.File
	fset *token.FileSet
}

func rewriteImports(dir string, upgrades []upgrade) error {
	upgradeMap := map[string]string{}
	for _, upgrade := range upgrades {
		upgradeMap[upgrade.oldPath] = upgrade.newPath
	}

	pkgs, err := loadPackages(dir)
	if err != nil {
		return fmt.Errorf("error loading packages: %s", err)
	}

	var (
		modified       = []file{}
		filesVisited   = map[string]bool{}
		importToModule = map[string]string{} // Cache of module paths
	)
	for _, pkg := range pkgs {
		if *verbose {
			fmt.Printf("Package: %s\n", pkg.PkgPath)
		}
		for i, fileAST := range pkg.Syntax {
			filename := pkg.CompiledGoFiles[i]

			// Skip this file if we've already visited it (including test
			// packages means some files can appear more than once)
			if filesVisited[filename] {
				continue
			}
			filesVisited[filename] = true

			var found bool
			for _, fileImp := range fileAST.Imports {
				importPath := strings.Trim(fileImp.Path.Value, "\"")

				// We have to actually compare module paths, not just import
				// paths. Imagine upgrading dep to dep/v5, but dep/v3 is
				// already installed. If we only looked at import paths, we'd
				// be liable to get dep/v5/v3, which is invalid. It's difficult
				// to tell where the module path ends and the package path
				// begins, so we call out to "go list" (and cache the result).
				modulePath, exists := importToModule[importPath]
				if !exists {
					modulePath, err = getModulePath(importPath)
					if err != nil {
						return fmt.Errorf("error getting module path for import %s: %s", importPath, err)
					}
					importToModule[importPath] = modulePath
				}

				if newPath, ok := upgradeMap[modulePath]; ok {
					if !found {
						found = true
						if *verbose {
							fmt.Printf("%s:\n", filename)
						}
					}

					newImportPath := strings.Replace(importPath, modulePath, newPath, 1)
					if err := module.CheckImportPath(newImportPath); err != nil {
						return fmt.Errorf("invalid import path after upgrade: %s", newImportPath)
					}
					fileImp.Path.Value = fmt.Sprintf("\"%s\"", newImportPath)

					if *verbose {
						fmt.Printf("\t%s -> %s\n", importPath, newImportPath)
					}
				}
			}

			// If any of the file's import paths were updated, write it to disk
			if found {
				modified = append(modified, file{
					name: filename,
					ast:  fileAST,
					fset: pkg.Fset,
				})
			}
		}
	}

	// Write modified files at the end, to avoid issues with "go list"
	// during the process (in case the upgrade breaks the build)
	for _, file := range modified {
		if err := writeFile(file); err != nil {
			return fmt.Errorf("error writing file: %s", err)
		}
	}
	return nil
}

func loadPackages(dir string) ([]*packages.Package, error) {
	cfg := &packages.Config{
		Mode:  packages.LoadSyntax,
		Tests: true,
	}
	loadPath := fmt.Sprintf("%s/...", path.Clean(dir))
	pkgs, err := packages.Load(cfg, loadPath)
	if err != nil {
		return nil, fmt.Errorf("error loading package info: %s", err)
	}

	if len(pkgs) < 1 {
		return nil, fmt.Errorf("failed to find/load package info")
	}

	return pkgs, nil
}

func writeFile(file file) error {
	f, err := os.Create(file.name)
	if err != nil {
		return fmt.Errorf("error opening file %s: %s", file.name, err)
	}
	defer f.Close()

	if err := printer.Fprint(f, file.fset, file.ast); err != nil {
		return fmt.Errorf("error writing file %s: %s", file.name, err)
	}

	return nil
}
