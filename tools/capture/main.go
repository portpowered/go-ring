package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	if err := runCLI(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCLI(arguments []string) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outputDirectory := flags.String("out", "", "fixture output directory (defaults to tests/replay/fixtures)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return captureErrorf("usage: go run ./tools/capture [-out fixture-directory] <capture-file>")
	}
	output := *outputDirectory
	if output == "" {
		output = filepath.Join(repositoryRoot(), "tests", "replay", "fixtures")
	}
	return extractCapture(flags.Arg(0), output)
}
