package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/portpowered/go-ring/tools/routegate"
)

func main() {
	root := flag.String("root", ".", "repository root to audit")
	flag.Parse()

	findings, err := routegate.Audit(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "routegate:", err)
		os.Exit(2)
	}

	for _, finding := range findings {
		fmt.Fprintln(os.Stderr, finding.String())
	}

	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "routegate: %d finding(s)\n", len(findings))
		os.Exit(1)
	}

	_, _ = fmt.Fprintln(os.Stdout, "routegate: contracts and outbound callsites are covered")
}
