package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/module"
	"golang.org/x/tools/go/packages"
)

type upgrade struct {
	oldPath string
	newPath string
}

type file struct {
	name string
	ast  *ast.File
	fset *token.FileSet
}

func rewriteImports(upgrades []upgrade) error {
	if len(upgrades) == 0 {
		return nil
	}

	upgradeMap := map[string]string{}
	for _, upgrade := range upgrades {
		upgradeMap[upgrade.oldPath] = upgrade.newPath
	}

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		return fmt.Errorf("error getting absolute path of module directory: %s", err)
	}
	// Trailing separator so sibling directories sharing a prefix don't match
	absDir += string(filepath.Separator)

	pkgs, err := loadPackages()
	if err != nil {
		return fmt.Errorf("error loading packages: %s", err)
	}

	var (
		modified     = []file{}
		filesVisited = map[string]bool{}
	)
	for _, pkg := range pkgs {
		if *verbose {
			fmt.Printf("Package: %s\n", pkg.PkgPath)
		}
		for i, fileAST := range pkg.Syntax {
			filename := pkg.CompiledGoFiles[i]

			// Skip the file if it isn't located within the module directory.
			// This is particularly important for preventing changes to "test
			// binary" files, which are typically located in the user's
			// $HOME/.cache/go-build/ directory, and should not be modified
			// (but are returned when loading test packages).
			// NOTE: This feels a little hacky, but I could not find a more
			// reliable way to identify the test binary package or ignore its
			// files. See: https://github.com/nathanjcochran/upgrade/issues/2.
			if !strings.HasPrefix(filename, absDir) {
				continue
			}

			// Skip the file if we've already visited it (including test
			// packages means some files can appear more than once)
			if filesVisited[filename] {
				continue
			}
			filesVisited[filename] = true

			var found bool
			for _, fileImp := range fileAST.Imports {
				importPath, err := strconv.Unquote(fileImp.Path.Value)
				if err != nil {
					return fmt.Errorf("error parsing import path %s: %s", fileImp.Path.Value, err)
				}

				// We have to actually compare module paths, not just import
				// path prefixes. Imagine upgrading dep to dep/v5, but dep/v3
				// is also installed. If we only looked at import paths, we'd
				// be liable to get dep/v5/v3, which is invalid.
				impPkg, exists := pkg.Imports[importPath]
				if !exists {
					return fmt.Errorf("error getting package information for import %s", importPath)
				}

				// NOTE: Some imports, such as standard library packages, do
				// not have a corresponding module. In these case, we default
				// to the package name as it was specified in the import
				// statement (it won't be updated).
				modulePath := importPath
				if impPkg.Module != nil {
					modulePath = impPkg.Module.Path
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
					fileImp.Path.Value = strconv.Quote(newImportPath)

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

func loadPackages() ([]*packages.Package, error) {
	cfg := &packages.Config{
		Dir: *dir,
		Mode: packages.NeedName |
			packages.NeedCompiledGoFiles |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedTypes |
			packages.NeedSyntax |
			packages.NeedModule,
		Tests: true, // Necessary to rewrite imports in _test.go files
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("error loading package info: %s", err)
	}

	if len(pkgs) < 1 {
		return nil, fmt.Errorf("failed to find/load package info")
	}

	return pkgs, nil
}

func writeFile(file file) error {
	// Format into memory first, so a formatting error can't leave behind a
	// truncated file
	var buf bytes.Buffer
	if err := format.Node(&buf, file.fset, file.ast); err != nil {
		return fmt.Errorf("error formatting file %s: %s", file.name, err)
	}

	if err := os.WriteFile(file.name, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("error writing file %s: %s", file.name, err)
	}
	return nil
}
