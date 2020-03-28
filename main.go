package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

var (
	filePath = flag.String("f", "./go.mod", "go.mod file path")
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

	// Validate and parse the module path
	prefix, currentMajor, ok := module.SplitPathVersion(path)
	if !ok {
		log.Fatalf("Invalid module path: %s", path)
	}

	// If no target major version was given, call 'go list -m'
	// to find the highest available major version
	// TODO: Validate target major
	targetMajor := flag.Arg(1)
	if targetMajor == "" {
		targetMajor = getTargetMajor(prefix, currentMajor)
	}

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

	// Get the full version for the upgraded dependency
	// (with the highest available minor/patch version)
	version := getFullVersion(newPath, targetMajor)

	// Drop the old module dependency and add the new, upgraded one
	if err := file.DropRequire(path); err != nil {
		log.Fatalf("Error dropping module requirement %s: %s", path, err)
	}
	if err := file.AddRequire(newPath, version); err != nil {
		log.Fatalf("Error adding module requirement %s: %s", newPath, err)
	}

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

func getTargetMajor(prefix, currentMajor string) string {
	// We're always upgrading, so start at v2
	version := 2

	// If the dependency already has a major version in its
	// import path, start there
	if currentMajor != "" {
		version, err := strconv.Atoi(currentMajor[1:])
		if err != nil {
			log.Fatalf("Invalid major version '%s': %s", currentMajor, err)
		}
		version++
	}

	var majorVersion string
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
			log.Fatalf("Error executing 'go list -m -e -json' command: %s", err)
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
				log.Fatalf("Error parsing results of 'go list -m -e -json' command: %s", err)
			}

			// TODO: Checking the content of the error message is hacky,
			// but it's the only way I could differentiate errors due to
			// incompatible pre-module versions from errors due to unavailable
			// (i.e. not yet released) versions.
			if result.Error.Err == "" {
				majorVersion = strings.Split(result.Version, ".")[0]
			} else if strings.Contains(result.Error.Err, "no matching versions for query") {
				if majorVersion == "" {
					log.Fatalf("No major versions available for upgrade")
				}
				if *verbose {
					fmt.Printf("Found major version: %s/%s\n", prefix, majorVersion)
				}
				return majorVersion
			} else if *verbose {
				fmt.Println(result.Error.Err)
			}
		}
	}
}

func getFullVersion(path, majorVersion string) string {
	cmd := exec.CommandContext(context.Background(),
		"go", "list", "-m", "-f", "{{.Version}}",
		fmt.Sprintf("%s@%s", path, majorVersion),
	)
	version, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			fmt.Println(string(err.Stderr))
		}
		log.Fatalf("Error executing 'go list -m -f {{.Version}}' command: %s", err)
	}

	return string(version)
}
