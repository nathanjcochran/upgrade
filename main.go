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
	modfilePath = flag.String("modfile", "go.mod", "Path to go.mod file")
	verbose     = flag.Bool("v", false, "Verbose output")
)

func main() {
	flag.Parse()

	switch flag.Arg(0) {
	case "upgrade":
		path := flag.Arg(1)
		if path == "" {
			flag.Usage()
			os.Exit(2)
		}
		targetMajor := flag.Arg(2)

		b, err := ioutil.ReadFile(*modfilePath)
		if err != nil {
			log.Fatal(err)
		}

		file, err := modfile.Parse(*modfilePath, b, nil)
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
	default:
		flag.Usage()
		os.Exit(2)
	}
}

const batchSize = 25

func getTargetMajor(prefix, currentMajor string) string {
	version := 2 // We're never updating to the earliest version
	if currentMajor != "" {
		version, err := strconv.Atoi(currentMajor[1:])
		if err != nil {
			log.Fatal(err)
		}
		version++
	}

	var majorVersion string
	for {
		var batch []string
		for i := 0; i < batchSize; i++ {
			majorVersion := fmt.Sprintf("v%d", version)
			batch = append(batch, fmt.Sprintf("%s/%s@%s", prefix, majorVersion, majorVersion))
			version++
		}

		args := []string{"list", "-m", "-e", "-json"}
		args = append(args, batch...)

		cmd := exec.CommandContext(context.Background(), "go", args...)
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
