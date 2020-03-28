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
	targetMajor := flag.Arg(1)

	b, err := ioutil.ReadFile(*filePath)
	if err != nil {
		log.Fatal(err)
	}

	file, err := modfile.Parse(*filePath, b, nil)
	if err != nil {
		log.Fatal(err)
	}

	//out, err := json.MarshalIndent(file, "", "  ")
	//if err != nil {
	//	log.Fatal(err)
	//}
	//fmt.Printf("%s\n", string(out))

	found := false
	for _, require := range file.Require {
		if require.Mod.Path == path {
			found = true
			prefix, currentMajor, ok := module.SplitPathVersion(require.Mod.Path)
			if !ok {
				log.Fatal("Invalid module path in go.mod file: %s", require.Mod.Path)
			}

			if targetMajor == "" {
				targetMajor = getTargetMajor(prefix, currentMajor)
			}

			newPath := fmt.Sprintf("%s/%s", prefix, targetMajor)
			file.SetRequire([]*modfile.Require{
				&modfile.Require{
					Mod: module.Version{
						Path:    newPath,
						Version: getMinorVersion(newPath, targetMajor),
					},
				},
			})
		}
	}

	if !found {
		log.Fatalf("Module not a known dependency: %s", path)
	}

	file.Cleanup()
	out, err := file.Format()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(out))

	//if err := ioutil.WriteFile("go.mod", out, 0644); err != nil {
	//	log.Fatal(err)
	//}
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
			log.Fatal(err)
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
			log.Fatal(err)
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
				log.Fatal(err)
			}

			// TODO: Checking the content of the error message is hacky,
			// but it's the only way I could differentiate errors due to
			// incompatible pre-module versions from errors due to unavailable
			// (i.e. not yet released) versions.
			if result.Error.Err == "" {
				majorVersion = strings.Split(result.Version, ".")[0]
			} else if strings.Contains(result.Error.Err, "no matching versions for query") {
				if majorVersion == "" {
					log.Fatal("No major versions available for upgrade")
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

func getMinorVersion(path, pathMajor string) string {
	cmd := exec.CommandContext(context.Background(),
		"go", "list", "-m", "-f", "{{.Version}}",
		fmt.Sprintf("%s@%s", path, pathMajor),
	)
	version, err := cmd.Output()
	if err != nil {
		if err := err.(*exec.ExitError); err != nil {
			fmt.Println(string(err.Stderr))
		}
		log.Fatal(err)
	}

	return string(version)
}
