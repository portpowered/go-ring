package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	err := runCLI(os.Args[1:])
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCLI(arguments []string) error {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	outputDirectory := flags.String("out", "", "fixture output directory (defaults to tests/replay/fixtures)")

	err := flags.Parse(arguments)
	if err != nil {
		return wrapCaptureError("parse capture command options", err)
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
