package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path"
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
				// path prefixes. Imagine upgrading dep to dep/v5, but dep/v3
				// is also installed. If we only looked at import paths, we'd
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

	if err := format.Node(f, file.fset, file.ast); err != nil {
		return fmt.Errorf("error writing file %s: %s", file.name, err)
	}

	return nil
}

func getModulePath(importPath string) (string, error) {
	results, err := listPackages(context.Background(), importPath)
	if err != nil {
		return "", fmt.Errorf("error getting package info: %s", err)
	}
	result := results[0]

	if result.Error != nil {
		return "", fmt.Errorf("error getting package info: %s", err)
	}

	// Standard library packages don't have a module
	if result.Standard {
		return importPath, nil
	}

	if result.Module == nil || result.Module.Path == "" {
		return "", fmt.Errorf("no module path returned in package info")
	}

	return result.Module.Path, nil
}
